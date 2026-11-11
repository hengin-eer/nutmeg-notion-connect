package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	stream := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"a","name":"Bash","input":{"command":"curl https://api.notion.com/v1/search"}}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"a","name":"Bash","input":{"command":"curl https://api.notion.com/v1/search"}}]}}
{"type":"result","subtype":"success","usage":{"input_tokens":12,"output_tokens":5,"cache_read_input_tokens":7,"cache_creation_input_tokens":3},"total_cost_usd":0.01,"duration_ms":100,"num_turns":2,"result":"done"}`
	var r result
	if err := parse(strings.NewReader(stream), &r); err != nil {
		t.Fatal(err)
	}
	if r.Input != 12 || r.Output != 5 || r.Read != 7 || r.Creation != 3 || r.Tools != 1 || r.API != 1 || r.Answer != "done" || r.Error {
		t.Fatalf("unexpected result %+v", r)
	}
}
func TestIncompleteStream(t *testing.T) {
	for _, s := range []string{"", `not JSON`, `{"type":"system"}`} {
		if parse(strings.NewReader(s), &result{}) == nil {
			t.Fatalf("accepted %q", s)
		}
	}
}
func TestRedact(t *testing.T) {
	s := redact("custom-key secret_abc123 ntn_xyz-123", "custom-key")
	if s != "[REDACTED] [REDACTED] [REDACTED]" {
		t.Fatal(s)
	}
}

func TestTrialIsolation(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "claude")
	script := `#!/bin/sh
[ "$PWD" != "` + dir + `" ] || exit 10
[ "$HOME" != "` + dir + `" ] || exit 11
[ -z "$UNRELATED_SECRET" ] || exit 12
if command -v nn >/dev/null 2>&1; then
 printf 'request\nrequest\n' > "$NN_STATS_FILE"
fi
printf '%s\n' '{"type":"result","subtype":"success","usage":{"input_tokens":1},"result":"secret_sensitive custom-key"}'
`
	if err := os.WriteFile(fake, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	nn := filepath.Join(dir, "nn")
	if err := os.WriteFile(nn, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("UNRELATED_SECRET", "must-not-inherit")
	for _, cond := range []string{"raw", "nn"} {
		r := trial(fake, nn, cond, cond, "haiku", "context", "custom-key", task{ID: "test", Prompt: "read", Expect: []string{"custom-key"}}, 1, .5, time.Second*5, trialOptions{Bare: true, NNCache: "cold"})
		if r.Error || r.Answer != "[REDACTED] [REDACTED]" || r.Matches != 1 || r.Isolation != "bare" || r.NNCache != "cold" {
			t.Fatalf("%s: %+v", cond, r)
		}
		if cond == "raw" && r.API != 0 || cond == "nn" && r.API != 2 {
			t.Fatalf("%s API=%d", cond, r.API)
		}
	}
}

func TestLoginAndCacheModes(t *testing.T) {
	home := t.TempDir()
	config := t.TempDir()
	cache := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "custom-claude"))
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("XDG_CACHE_HOME", cache)
	for p, body := range map[string]string{
		filepath.Join(config, "nutmeg-notion-connect/aliases.json"): `{"example":"local"}`,
		filepath.Join(config, "nutmeg-notion-connect/credentials"):  "must-not-copy",
		filepath.Join(cache, "nutmeg-notion-connect/nested/page"):   "cached page",
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	fake := filepath.Join(t.TempDir(), "claude")
	script := `#!/bin/sh
[ "$HOME" = '` + home + `' ] || exit 10
[ "$CLAUDE_CONFIG_DIR" = '` + filepath.Join(home, "custom-claude") + `' ] || exit 11
[ "$XDG_CONFIG_HOME" != '` + config + `' ] || exit 12
[ "$XDG_CACHE_HOME" != '` + cache + `' ] || exit 13
[ ! -e "$XDG_CONFIG_HOME/nutmeg-notion-connect/credentials" ] || exit 14
for arg in "$@"; do [ "$arg" != --bare ] || exit 15; done
if [ -f "$XDG_CACHE_HOME/nutmeg-notion-connect/nested/page" ]; then
 [ -f "$XDG_CONFIG_HOME/nutmeg-notion-connect/aliases.json" ] || exit 16
 printf 'changed' > "$XDG_CACHE_HOME/nutmeg-notion-connect/nested/page"
 printf 'changed' > "$XDG_CONFIG_HOME/nutmeg-notion-connect/aliases.json"
 printf '%s\n' '{"type":"result","subtype":"success","result":"warm"}'
else
 [ ! -e "$XDG_CONFIG_HOME/nutmeg-notion-connect/aliases.json" ] || exit 17
 printf '%s\n' '{"type":"result","subtype":"success","result":"cold"}'
fi
`
	if err := os.WriteFile(fake, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"warm", "cold"} {
		r := trial(fake, fake, "nn", "nn", "haiku", "context", "key", task{ID: "test", Prompt: "read"}, 1, .5, time.Second*5, trialOptions{NNCache: mode})
		if r.Error || r.Answer != mode || r.NNCache != mode || r.Isolation != "login" {
			t.Fatalf("%s: %+v", mode, r)
		}
	}
	for p, want := range map[string]string{filepath.Join(cache, "nutmeg-notion-connect/nested/page"): "cached page", filepath.Join(config, "nutmeg-notion-connect/aliases.json"): `{"example":"local"}`} {
		b, err := os.ReadFile(p)
		if err != nil || string(b) != want {
			t.Fatalf("source modified: %s %v", p, err)
		}
	}
}
func TestBareRequiresAPIKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	err := run([]string{"--bare", "--tasks", "does-not-exist"})
	if err == nil || err.Error() != "--bare requires ANTHROPIC_API_KEY" {
		t.Fatalf("expected early auth error, got %v", err)
	}
}
func TestWarmMissingSources(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := seedNN(t.TempDir()); err != nil {
		t.Fatal(err)
	}
}

func TestReportSeparatesModes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "results.jsonl")
	var data strings.Builder
	for _, mode := range []struct{ isolation, cache string }{{"login", "warm"}, {"login", "cold"}, {"bare", "warm"}} {
		r := result{Condition: "nn", Model: "haiku", Task: "test", Isolation: mode.isolation, NNCache: mode.cache}
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		data.Write(b)
		data.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(data.String()), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := os.CreateTemp(dir, "report")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	original := os.Stdout
	func() { os.Stdout = out; defer func() { os.Stdout = original }(); err = report([]string{"--in", path}) }()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out.Name())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"nn / haiku / test / login / warm", "nn / haiku / test / login / cold", "nn / haiku / test / bare / warm"} {
		if !strings.Contains(string(b), "| "+key+" | 1 (0) |") {
			t.Fatalf("missing group %s: %s", key, b)
		}
	}
}
