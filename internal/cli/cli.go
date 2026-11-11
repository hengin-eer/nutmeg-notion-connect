// Package cli wires the nn subcommands to the internal packages.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hengin-eer/nutmeg-notion-connect/internal/config"
	"github.com/hengin-eer/nutmeg-notion-connect/internal/index"
	"github.com/hengin-eer/nutmeg-notion-connect/internal/notion"
	"github.com/hengin-eer/nutmeg-notion-connect/internal/pagecache"
)

const (
	// Pages longer than this many characters return their outline instead,
	// about 6-8k tokens of Japanese text.
	defaultMax = 12000
	// find/dbs/overview sync the index first when it is older than this.
	syncTTL = 10 * time.Minute
)

type app struct {
	out     io.Writer
	errOut  io.Writer
	aliases config.Aliases
	client  *notion.Client
	version string
}

// exitError carries a message for stderr and a non-zero exit code.
type exitError struct{ msg string }

func (e exitError) Error() string { return e.msg }

func fail(format string, a ...any) error { return exitError{fmt.Sprintf(format, a...)} }

type command struct {
	name, args, help string
	run              func(a *app, args []string) error
}

var commands []command

func init() {
	commands = []command{
		{"overview", "[--db X] [--top N] [--days N]", "ワークスペースの全体像（索引から集計。API0回）", (*app).cmdOverview},
		{"find", "単語... [--db X] [--type page|db] [--since YYYY-MM-DD] [--limit N]", "タイトルで探す（索引を使う）", (*app).cmdFind},
		{"outline", "<id> [--cached]", "見出しだけ（各セクションの行数つき）", (*app).cmdOutline},
		{"read", "<id> [-s 見出し] [--lines 20-60] [--links] [--fresh] [--max N] [--all] [--cached]", "ページをmarkdownで読む", (*app).cmdRead},
		{"grep", "<id> <正規表現> [--cached]", "ページ内を探す（どの見出しの中か付き）", (*app).cmdGrep},
		{"children", "<id> [--cached]", "子ページ・子DB・リンクの一覧", (*app).cmdChildren},
		{"diff", "<id> [-C N]", "前回読んだ版との差分", (*app).cmdDiff},
		{"dbs", "[単語...]", "DBの一覧", (*app).cmdDBs},
		{"db", "<id> [--schema] [--title X] [--where 列~x|列=x] [--since D] [--props a,b] [--filter-json J] [--limit N]", "DBの行をTSVで", (*app).cmdDB},
		{"alias", "[名前 [<id>]]", "別名の一覧／登録／削除", (*app).cmdAlias},
		{"sync", "[--full]", "索引を更新（--full で作り直し）", (*app).cmdSync},
		{"auth", "login|status|logout", "キーの登録・確認・削除", (*app).cmdAuth},
		{"version", "", "バージョン", (*app).cmdVersion},
	}
}

// Main runs nn and returns the process exit code.
func Main(args []string, version string) int {
	a := &app{out: os.Stdout, errOut: os.Stderr, aliases: config.LoadAliases(), version: version}
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		a.usage()
		return 0
	}
	for _, c := range commands {
		if c.name == args[0] {
			err := c.run(a, args[1:])
			if err == nil || errors.Is(err, flag.ErrHelp) {
				return 0
			}
			fmt.Fprintln(a.errOut, "エラー:", err)
			return 1
		}
	}
	fmt.Fprintf(a.errOut, "不明なコマンド: %s\n", args[0])
	a.usage()
	return 2
}

func (a *app) usage() {
	fmt.Fprintln(a.out, "nn: NUTMEG の Notion を読み取り専用で読む CLI。<id> には Notion の URL・ID・別名を渡せる")
	fmt.Fprintln(a.out)
	for _, c := range commands {
		fmt.Fprintf(a.out, "  nn %s %s\n      %s\n", c.name, c.args, c.help)
	}
}

// parse lets flags appear before or after positional arguments.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return pos, nil
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
}

func newFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet("nn "+name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	return fs
}

func (a *app) api() *notion.Client {
	if a.client != nil {
		return a.client
	}
	key, _, err := config.LoadKey()
	if err != nil {
		fmt.Fprintln(a.errOut, "エラー: キーが登録されていません。自分のターミナルで `nn auth login` を実行してください（環境変数 "+config.EnvKey+" でも可）")
		os.Exit(1)
	}
	a.client = notion.New(key)
	return a.client
}

func (a *app) store() *pagecache.Store {
	return &pagecache.Store{Dir: filepath.Join(config.CacheDir(), "pages"), Client: a.api}
}

func (a *app) resolve(s string) (string, error) { return a.aliases.Resolve(s) }

func indexPath() string { return filepath.Join(config.CacheDir(), "index.json") }

func (a *app) aliasIDs() []string {
	var ids []string
	for _, n := range a.aliases.Names() {
		if id, err := config.NormID(a.aliases[n]); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// ensureIndex syncs the index if it is missing or older than syncTTL.
func (a *app) ensureIndex() (*index.Index, error) {
	ix, err := index.Load(indexPath())
	if err != nil {
		return nil, err
	}
	age, ok := ix.Age()
	if ok && age < syncTTL {
		return ix, nil
	}
	var progress io.Writer
	if !ok {
		fmt.Fprintln(a.errOut, "初回の索引づくり中（1分ほどかかります）...")
		progress = a.errOut
	}
	if _, err := ix.Sync(a.api(), false, a.aliasIDs(), progress); err != nil {
		return nil, err
	}
	return ix, nil
}

func header(m pagecache.Meta, body string) string {
	return fmt.Sprintf("%s  [%s / 更新 %s / %d字]", m.Title, m.ID, notion.JST(m.Edited), len([]rune(body)))
}

func date(iso string) string { return notion.JST(iso)[:min(10, len(notion.JST(iso)))] }

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func splitLines(s string) []string { return strings.Split(strings.TrimSuffix(s, "\n"), "\n") }
