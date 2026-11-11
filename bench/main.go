// Command bench measures isolated Claude Code trials. Standard library only.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type task struct {
	ID     string   `json:"id"`
	Prompt string   `json:"prompt"`
	Expect []string `json:"expect,omitempty"`
}
type usage struct {
	Input    int `json:"input_tokens"`
	Output   int `json:"output_tokens"`
	Read     int `json:"cache_read_input_tokens"`
	Creation int `json:"cache_creation_input_tokens"`
}
type result struct {
	Date      string `json:"date"`
	Condition string `json:"condition"`
	Model     string `json:"model"`
	Task      string `json:"task_id"`
	Trial     int    `json:"trial"`
	Isolation string `json:"isolation"`
	NNCache   string `json:"nn_cache"`
	usage
	Cost      float64 `json:"total_cost_usd"`
	Duration  int64   `json:"duration_ms"`
	Wall      int64   `json:"wall_ms"`
	Turns     int     `json:"num_turns"`
	Tools     int     `json:"tool_calls"`
	API       int     `json:"notion_api_calls"`
	Estimated bool    `json:"notion_api_estimated"`
	Answer    string  `json:"answer"`
	Error     bool    `json:"error"`
	Detail    string  `json:"error_detail,omitempty"`
	Matches   int     `json:"expect_matches"`
	Expected  int     `json:"expect_total"`
}

var token = regexp.MustCompile(`(?:secret_|ntn_)[A-Za-z0-9_-]+`)

func redact(s, key string) string {
	if key != "" {
		s = strings.ReplaceAll(s, key, "[REDACTED]")
	}
	return token.ReplaceAllString(s, "[REDACTED]")
}
func main() {
	if err := mainErr(); err != nil {
		fmt.Fprintln(os.Stderr, redact(err.Error(), os.Getenv("NUTMEG_NOTION_KEY")))
		os.Exit(1)
	}
}
func mainErr() error {
	if len(os.Args) < 2 {
		return errors.New("usage: go run ./bench run|report [flags]")
	}
	switch os.Args[1] {
	case "run":
		return run(os.Args[2:])
	case "report":
		return report(os.Args[2:])
	default:
		return errors.New("unknown subcommand")
	}
}
func loadKey() (string, error) {
	if k := strings.TrimSpace(os.Getenv("NUTMEG_NOTION_KEY")); k != "" {
		return k, nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(filepath.Join(h, ".config/nutmeg-notion-connect/credentials"))
	if err != nil {
		return "", errors.New("Notion key unavailable")
	}
	k := strings.TrimSpace(string(b))
	if k == "" {
		return "", errors.New("empty Notion key")
	}
	return k, nil
}
func run(args []string) error {
	f := flag.NewFlagSet("run", flag.ContinueOnError)
	bare := f.Bool("bare", false, "fully isolate Claude; requires ANTHROPIC_API_KEY")
	nnCache := f.String("nn-cache", "warm", "warm or cold nn cache")
	cond := f.String("cond", "raw", "raw or nn")
	label := f.String("label", "", "condition label")
	model := f.String("model", "haiku", "Claude model")
	tasks := f.String("tasks", "bench/tasks.local.json", "task JSON array")
	id := f.String("task", "", "only this task ID")
	out := f.String("out", "bench/results/results.jsonl", "JSONL destination")
	bin := f.String("nn-bin", "bin/nn", "nn binary")
	skill := f.String("skill", "skills/nutmeg-notion-connect/SKILL.md", "skill file")
	n := f.Int("trials", 1, "trials per task")
	budget := f.Float64("max-budget-usd", 0.5, "per-trial budget")
	timeout := f.Duration("timeout", 10*time.Minute, "trial timeout")
	if err := f.Parse(args); err != nil {
		return err
	}
	if (*cond != "raw" && *cond != "nn") || *n < 1 || *budget <= 0 || math.IsNaN(*budget) || math.IsInf(*budget, 0) || *timeout <= 0 {
		return errors.New("invalid condition, trials, budget or timeout")
	}
	if *bare && strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")) == "" {
		return errors.New("--bare requires ANTHROPIC_API_KEY")
	}
	if *nnCache != "warm" && *nnCache != "cold" {
		return errors.New("--nn-cache must be warm or cold")
	}
	b, err := os.ReadFile(*tasks)
	if err != nil {
		return err
	}
	var ts []task
	if err = json.Unmarshal(b, &ts); err != nil {
		return err
	}
	selected := []task{}
	seen := map[string]bool{}
	for _, t := range ts {
		if t.ID == "" || t.Prompt == "" || seen[t.ID] {
			return errors.New("tasks require unique IDs and nonempty prompts")
		}
		seen[t.ID] = true
		if *id == "" || t.ID == *id {
			selected = append(selected, t)
		}
	}
	if len(selected) == 0 {
		return errors.New("no tasks selected")
	}
	key, err := loadKey()
	if err != nil {
		return err
	}
	claude, err := exec.LookPath("claude")
	if err != nil {
		return err
	}
	claude, err = filepath.Abs(claude)
	if err != nil {
		return err
	}
	prompt := "Use Notion read-only. Never write or modify Notion data. Never print credentials. The Notion API key is in $NOTION_KEY; use curl or other tools to call the API directly."
	if *cond == "nn" {
		b, err = os.ReadFile(*skill)
		if err != nil {
			return err
		}
		prompt = "Use Notion read-only. Never print credentials. Follow this skill (explicitly supplied in isolated mode):\n" + string(b)
		*bin, err = filepath.Abs(*bin)
		if err != nil {
			return err
		}
		if _, err = os.Stat(*bin); err != nil {
			return err
		}
	}
	if *label == "" {
		*label = *cond
	}
	if err = os.MkdirAll(filepath.Dir(*out), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(*out, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	failed := false
	for _, t := range selected {
		for i := 1; i <= *n; i++ {
			r := trial(claude, *bin, *cond, *label, *model, prompt, key, t, i, *budget, *timeout, trialOptions{Bare: *bare, NNCache: *nnCache})
			r.Condition = redact(r.Condition, key)
			r.Model = redact(r.Model, key)
			r.Task = redact(r.Task, key)
			data, err := json.Marshal(r)
			if err != nil {
				return err
			}
			if _, err = file.WriteString(redact(string(data), key) + "\n"); err != nil {
				return err
			}
			if err = file.Sync(); err != nil {
				return err
			}
			fmt.Printf("%s: trial %d error=%t\n", redact(t.ID, key), i, r.Error)
			failed = failed || r.Error
		}
	}
	if failed {
		return errors.New("one or more trials failed; see redacted results")
	}
	return nil
}

type trialOptions struct {
	Bare    bool
	NNCache string
}

// Seed only nn data into private XDG directories; never copy credentials.
func seedNN(dir string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	cache := os.Getenv("XDG_CACHE_HOME")
	if cache == "" {
		cache = filepath.Join(home, ".cache")
	}
	config := os.Getenv("XDG_CONFIG_HOME")
	if config == "" {
		config = filepath.Join(home, ".config")
	}
	if err = copyPrivate(filepath.Join(cache, "nutmeg-notion-connect"), filepath.Join(dir, "cache/nutmeg-notion-connect")); err != nil {
		return err
	}
	return copyPrivate(filepath.Join(config, "nutmeg-notion-connect/aliases.json"), filepath.Join(dir, "config/nutmeg-notion-connect/aliases.json"))
}
func copyPrivate(src, dst string) error {
	info, err := os.Lstat(src)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() {
		if err = os.MkdirAll(dst, 0700); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err = copyPrivate(filepath.Join(src, entry.Name()), filepath.Join(dst, entry.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("cache source must be a regular file or directory: %s", src)
	}
	if err = os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	closeErr := out.Close()
	if err != nil {
		return err
	}
	return closeErr
}
func trial(claude, bin, cond, label, model, prompt, key string, t task, i int, budget float64, timeout time.Duration, opts trialOptions) (r result) {
	r = result{Date: time.Now().UTC().Format(time.RFC3339Nano), Condition: label, Model: model, Task: t.ID, Trial: i, Estimated: cond == "raw", Expected: len(t.Expect)}
	r.Isolation = "login"
	if opts.Bare {
		r.Isolation = "bare"
	}
	r.NNCache = opts.NNCache
	defer func() { r.Answer = redact(r.Answer, key); r.Detail = redact(r.Detail, key) }()
	dir, err := os.MkdirTemp("", "nn-bench-")
	if err != nil {
		r.Error = true
		r.Detail = err.Error()
		return
	}
	defer os.RemoveAll(dir)
	for _, d := range []string{"home", "config", "cache", "bin", "claude"} {
		if err = os.MkdirAll(filepath.Join(dir, d), 0o700); err != nil {
			r.Error = true
			r.Detail = err.Error()
			return
		}
	}
	if cond == "nn" && opts.NNCache == "warm" {
		if err = seedNN(dir); err != nil {
			r.Error = true
			r.Detail = err.Error()
			return
		}
	}
	// A private tool directory excludes every pre-existing nn and shell startup file.
	for _, name := range []string{"bash", "sh", "curl", "python3", "jq", "env", "cat", "head", "tail", "wc", "sed", "grep", "sort", "date"} {
		if p, e := exec.LookPath(name); e == nil {
			if e = os.Symlink(p, filepath.Join(dir, "bin", name)); e != nil {
				r.Error = true
				r.Detail = e.Error()
				return
			}
		}
	}
	if cond == "nn" {
		if err = os.Symlink(bin, filepath.Join(dir, "bin/nn")); err != nil {
			r.Error = true
			r.Detail = err.Error()
			return
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	args := []string{"-p", t.Prompt, "--output-format", "stream-json", "--verbose", "--model", model, "--no-session-persistence", "--max-budget-usd", fmt.Sprint(budget), "--setting-sources", "", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--disable-slash-commands", "--tools", "Bash", "--allowedTools", "Bash", "--permission-mode", "dontAsk", "--append-system-prompt", prompt}
	if opts.Bare {
		args = append(args, "--bare")
	}
	cmd := exec.CommandContext(ctx, claude, args...)
	cmd.Dir = dir
	cmd.WaitDelay = 2 * time.Second
	cmd.Env = []string{"XDG_CONFIG_HOME=" + filepath.Join(dir, "config"), "XDG_CACHE_HOME=" + filepath.Join(dir, "cache"), "PATH=" + filepath.Join(dir, "bin"), "NOTION_KEY=" + key, "NUTMEG_NOTION_KEY=" + key, "NN_STATS_FILE=" + filepath.Join(dir, "stats"), "BASH_ENV=/dev/null"}
	if opts.Bare {
		cmd.Env = append(cmd.Env, "HOME="+filepath.Join(dir, "home"), "CLAUDE_CONFIG_DIR="+filepath.Join(dir, "claude"))
	} else {
		for _, k := range []string{"HOME", "CLAUDE_CONFIG_DIR", "CLAUDE_CODE_OAUTH_TOKEN"} {
			if v, ok := os.LookupEnv(k); ok {
				cmd.Env = append(cmd.Env, k+"="+v)
			}
		}
	}
	for _, k := range []string{"ANTHROPIC_API_KEY", "HTTPS_PROXY", "HTTP_PROXY", "NO_PROXY", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if v, ok := os.LookupEnv(k); ok {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		r.Error = true
		r.Detail = err.Error()
		return
	}
	// stderr is discarded: provider errors may contain secrets; structured errors are retained.
	cmd.Stderr = io.Discard
	start := time.Now()
	if err = cmd.Start(); err != nil {
		r.Error = true
		r.Detail = err.Error()
		return
	}
	parseErr := parse(pipe, &r)
	if parseErr != nil {
		_ = cmd.Process.Kill()
	}
	err = cmd.Wait()
	r.Wall = time.Since(start).Milliseconds()
	if parseErr != nil {
		r.Error = true
		r.Detail = parseErr.Error()
	}
	if err != nil {
		r.Error = true
		r.Detail += " " + err.Error()
	}
	if ctx.Err() != nil {
		r.Error = true
		r.Detail += " " + ctx.Err().Error()
	}
	if cond == "nn" {
		b, e := os.ReadFile(filepath.Join(dir, "stats"))
		if e != nil && !os.IsNotExist(e) {
			r.Error = true
			r.Detail += " stats unavailable"
		}
		r.API = strings.Count(string(b), "\n")
	}
	for _, s := range t.Expect {
		if strings.Contains(r.Answer, s) {
			r.Matches++
		}
	}
	return
}
func parse(in io.Reader, r *result) error {
	s := bufio.NewScanner(in)
	s.Buffer(make([]byte, 65536), 16*1024*1024)
	found := false
	seen := map[string]bool{}
	for s.Scan() {
		var e struct {
			Type string `json:"type"`
			usage
			Cost     float64 `json:"total_cost_usd"`
			Duration int64   `json:"duration_ms"`
			Turns    int     `json:"num_turns"`
			Result   string  `json:"result"`
			IsError  bool    `json:"is_error"`
			Subtype  string  `json:"subtype"`
			Usage    usage   `json:"usage"`
			Message  struct {
				Content []struct {
					Type  string `json:"type"`
					ID    string `json:"id"`
					Name  string `json:"name"`
					Input struct {
						Command string `json:"command"`
					} `json:"input"`
				} `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(s.Bytes(), &e); err != nil {
			return errors.New("invalid stream JSON")
		}
		if e.Type == "assistant" {
			for _, c := range e.Message.Content {
				if c.Type == "tool_use" && !seen[c.ID] {
					seen[c.ID] = true
					r.Tools++
					if c.Name == "Bash" {
						r.API += strings.Count(c.Input.Command, "api.notion.com")
					}
				}
			}
		}
		if e.Type == "result" {
			found = true
			r.usage = e.Usage
			r.Cost = e.Cost
			r.Duration = e.Duration
			r.Turns = e.Turns
			r.Answer = e.Result
			r.Error = e.IsError || e.Subtype != "success"
			if r.Error {
				r.Detail = e.Subtype
			}
		}
	}
	if err := s.Err(); err != nil {
		return err
	}
	if !found {
		return errors.New("stream ended without result")
	}
	return nil
}
func md(s string) string {
	return strings.NewReplacer("|", "\\|", "\n", "<br>", "\r", "", "<", "&lt;", ">", "&gt;").Replace(s)
}
func report(args []string) error {
	f := flag.NewFlagSet("report", flag.ContinueOnError)
	in := f.String("in", "bench/results/results.jsonl", "JSONL input")
	if err := f.Parse(args); err != nil {
		return err
	}
	file, err := os.Open(*in)
	if err != nil {
		return err
	}
	defer file.Close()
	key, _ := loadKey()
	s := bufio.NewScanner(file)
	s.Buffer(make([]byte, 65536), 16*1024*1024)
	groups := map[string][]result{}
	var rows []result
	for s.Scan() {
		var r result
		if err = json.Unmarshal(s.Bytes(), &r); err != nil {
			return errors.New("invalid result JSONL")
		}
		rows = append(rows, r)
		k := r.Condition + " / " + r.Model + " / " + r.Task + " / " + r.Isolation + " / " + r.NNCache
		groups[k] = append(groups[k], r)
	}
	if err = s.Err(); err != nil {
		return err
	}
	fmt.Println("| Condition / model / task / isolation / nn_cache | N (errors) | Mean input | output | cache read | cache creation | USD | ms | turns | tools | API |\n|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|")
	keys := []string{}
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		rs := groups[k]
		var a [9]float64
		errs := 0
		estimated := false
		for _, r := range rs {
			if r.Error {
				errs++
			}
			estimated = estimated || r.Estimated
			v := []float64{float64(r.Input), float64(r.Output), float64(r.Read), float64(r.Creation), r.Cost, float64(r.Duration), float64(r.Turns), float64(r.Tools), float64(r.API)}
			for i, x := range v {
				a[i] += x / float64(len(rs))
			}
		}
		suffix := ""
		if estimated {
			suffix = " (estimate)"
		}
		fmt.Printf("| %s | %d (%d) | %.1f | %.1f | %.1f | %.1f | %.6f | %.1f | %.1f | %.1f | %.1f%s |\n", md(redact(k, key)), len(rs), errs, a[0], a[1], a[2], a[3], a[4], a[5], a[6], a[7], a[8], suffix)
	}
	fmt.Println("\nAnswers (human review; keyword matches are only a reference):\n\n| Condition | Model | Task | Isolation | nn_cache | Trial | Error | Expect | Answer |\n|---|---|---|---|---|---:|---|---|---|")
	for _, r := range rows {
		fmt.Printf("| %s | %s | %s | %s | %s | %d | %t %s | %d/%d | %s |\n", md(redact(r.Condition, key)), md(redact(r.Model, key)), md(redact(r.Task, key)), md(r.Isolation), md(r.NNCache), r.Trial, r.Error, md(redact(r.Detail, key)), r.Matches, r.Expected, md(redact(r.Answer, key)))
	}
	return nil
}
