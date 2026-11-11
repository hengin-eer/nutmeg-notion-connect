package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/term"

	"github.com/hengin-eer/nutmeg-notion-connect/internal/config"
	"github.com/hengin-eer/nutmeg-notion-connect/internal/index"
	"github.com/hengin-eer/nutmeg-notion-connect/internal/notion"
	"github.com/hengin-eer/nutmeg-notion-connect/internal/pagecache"
	"github.com/hengin-eer/nutmeg-notion-connect/internal/render"
	"github.com/hengin-eer/nutmeg-notion-connect/internal/textdiff"
)

func (a *app) cmdOverview(args []string) error {
	fs := newFlags("overview")
	db := fs.String("db", "", "このDBの中だけ（IDか別名）")
	top := fs.Int("top", 10, "各項目の行数")
	days := fs.Int("days", 30, "「最近」の日数")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	ix, err := a.ensureIndex()
	if err != nil {
		return err
	}
	opt := index.OverviewOptions{Top: *top, Days: *days, Label: a.aliases.Of}
	if *db != "" {
		if opt.InDB, err = a.resolve(*db); err != nil {
			return err
		}
	}
	fmt.Fprint(a.out, ix.Overview(opt))
	return nil
}

func (a *app) cmdFind(args []string) error {
	fs := newFlags("find")
	db := fs.String("db", "", "このDBの中だけ（IDか別名）")
	kind := fs.String("type", "", "page か db")
	since := fs.String("since", "", "この日付（YYYY-MM-DD）以降に更新")
	limit := fs.Int("limit", 20, "最大件数")
	words, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(words) == 0 {
		return fail("探す単語を指定してください")
	}
	ix, err := a.ensureIndex()
	if err != nil {
		return err
	}
	opt := index.FindOptions{Words: words, Kind: *kind, Since: *since}
	if *db != "" {
		if opt.InDB, err = a.resolve(*db); err != nil {
			return err
		}
	}
	hits := ix.Find(opt)
	for _, h := range hits[:min(len(hits), *limit)] {
		mark := ""
		if h.Kind == "db" {
			mark = "🗃 "
		}
		line := fmt.Sprintf("%s  %s  %s%s", h.ID, date(h.Edited), mark, h.Title)
		if pl := a.parentLabel(ix, h.Parent); pl != "" {
			line += "  [" + pl + "]"
		}
		fmt.Fprintln(a.out, line)
	}
	if len(hits) > *limit {
		fmt.Fprintf(a.out, "…ほか%d件（--limit で増やせる）\n", len(hits)-*limit)
	}
	if len(hits) == 0 {
		fmt.Fprintln(a.out, "見つかりません。単語を減らすか、`nn sync --full` で索引を作り直してください")
	}
	return nil
}

func (a *app) parentLabel(ix *index.Index, id string) string {
	if id == "" {
		return ""
	}
	if al := a.aliases.Of(id); al != "" {
		return al
	}
	if p := ix.Items[id]; p != nil && p.Kind == "db" {
		r := []rune(p.Title)
		return string(r[:min(len(r), 20)])
	}
	return ""
}

// loadPage parses <id> (plus nPos-1 more positionals) and loads the page.
func (a *app) loadPage(name string, args []string, nPos int, extra func(fs *flagSet)) (string, pagecache.Meta, []string, error) {
	fs := newFlags(name)
	cached := fs.Bool("cached", false, "更新確認もせずキャッシュを使う（API0回）")
	links := false
	fresh := false
	if extra != nil {
		extra(&flagSet{fs, &links, &fresh})
	}
	pos, err := parse(fs, args)
	if err != nil {
		return "", pagecache.Meta{}, nil, err
	}
	if len(pos) != nPos {
		return "", pagecache.Meta{}, nil, fail("引数の数が違います。`nn -h` を見てください")
	}
	id, err := a.resolve(pos[0])
	if err != nil {
		return "", pagecache.Meta{}, nil, err
	}
	body, meta, err := a.store().Load(id, pagecache.Options{Links: links, Fresh: fresh, CachedOnly: *cached})
	return body, meta, pos[1:], err
}

// flagSet lets read add its own flags, including the cache options.
type flagSet struct {
	*flag.FlagSet
	links, fresh *bool
}

func (a *app) cmdOutline(args []string) error {
	body, meta, _, err := a.loadPage("outline", args, 1, nil)
	if err != nil {
		return err
	}
	fmt.Fprintln(a.out, header(meta, body))
	hs := render.Headings(body)
	for _, h := range hs {
		fmt.Fprintf(a.out, "%s%s %s  (L%d, %d行)\n", strings.Repeat("  ", h.Level-1), strings.Repeat("#", h.Level), h.Text, h.Line, h.End-h.Line)
	}
	if len(hs) == 0 {
		fmt.Fprintf(a.out, "(見出しなし、%d行)\n", len(splitLines(body)))
	}
	return nil
}

func (a *app) cmdRead(args []string) error {
	var section, lines *string
	var max *int
	var all *bool
	body, meta, _, err := a.loadPage("read", args, 1, func(f *flagSet) {
		section = f.String("section", "", "この文字を含む見出しのセクションだけ")
		f.StringVar(section, "s", "", "--section の短縮")
		lines = f.String("lines", "", "行の範囲（例: 20-60）")
		f.BoolVar(f.links, "links", false, "リンクのURLも出す")
		f.BoolVar(f.fresh, "fresh", false, "キャッシュを使わず取り直す")
		max = f.Int("max", defaultMax, "これより長いと見出し一覧だけ返す")
		all = f.Bool("all", false, "長くても全部出す")
	})
	if err != nil {
		return err
	}
	out := body
	if *section != "" {
		s, ok := render.Section(body, *section)
		if !ok {
			fmt.Fprintf(a.out, "見出し「%s」が見つかりません。見出し一覧:\n", *section)
			for _, h := range render.Headings(body) {
				fmt.Fprintln(a.out, strings.Repeat("  ", h.Level-1)+h.Text)
			}
			return nil
		}
		out = s
	}
	if *lines != "" {
		from, to, _ := strings.Cut(*lines, "-")
		ls := splitLines(out)
		s, err1 := strconv.Atoi(from)
		e := len(ls)
		if to != "" {
			var err2 error
			if e, err2 = strconv.Atoi(to); err2 != nil {
				err1 = err2
			}
		}
		if err1 != nil || s < 1 {
			return fail("--lines は 20-60 のように指定してください")
		}
		e = min(e, len(ls))
		if s > e {
			out = ""
		} else {
			out = strings.Join(ls[s-1:e], "\n") + "\n"
		}
	}
	fmt.Fprintln(a.out, header(meta, body))
	if n := len([]rune(out)); n > *max && !*all {
		fmt.Fprintf(a.out, "（%d字あるので全文は出しません。-s で見出しを指定するか、--all で全部出す）\n", n)
		for _, h := range render.Headings(out) {
			fmt.Fprintln(a.out, strings.Repeat("  ", h.Level-1)+strings.Repeat("#", h.Level)+" "+h.Text)
		}
		return nil
	}
	fmt.Fprint(a.out, out)
	return nil
}

var mdHeading = regexp.MustCompile(`^(#{1,3}) (.*)`)

func (a *app) cmdGrep(args []string) error {
	body, meta, rest, err := a.loadPage("grep", args, 2, nil)
	if err != nil {
		return err
	}
	rx, err := regexp.Compile("(?i)" + rest[0])
	if err != nil {
		return fail("正規表現として読めません: %v", err)
	}
	fmt.Fprintln(a.out, header(meta, body))
	path := map[int]string{}
	n := 0
	for i, l := range splitLines(body) {
		if m := mdHeading.FindStringSubmatch(l); m != nil {
			lv := len(m[1])
			for k := range path {
				if k >= lv {
					delete(path, k)
				}
			}
			path[lv] = m[2]
		}
		if rx.MatchString(l) {
			var ctx []string
			for lv := 1; lv <= 3; lv++ {
				if p, ok := path[lv]; ok {
					ctx = append(ctx, p)
				}
			}
			fmt.Fprintf(a.out, "L%d [%s] %s\n", i+1, strings.Join(ctx, " > "), strings.TrimSpace(l))
			n++
		}
	}
	if n == 0 {
		fmt.Fprintln(a.out, "当たりなし")
	}
	return nil
}

func (a *app) cmdChildren(args []string) error {
	body, meta, _, err := a.loadPage("children", args, 1, nil)
	if err != nil {
		return err
	}
	fmt.Fprintln(a.out, header(meta, body))
	n := 0
	for _, l := range splitLines(body) {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "📄") || strings.HasPrefix(t, "🗃") || strings.HasPrefix(t, "→") {
			fmt.Fprintln(a.out, t)
			n++
		}
	}
	if n == 0 {
		fmt.Fprintln(a.out, "子ページなし")
	}
	return nil
}

func (a *app) cmdDiff(args []string) error {
	fs := newFlags("diff")
	ctx := fs.Int("C", 1, "前後に出す行数")
	fs.IntVar(ctx, "context", 1, "-C と同じ")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fail("nn diff <id>")
	}
	id, err := a.resolve(pos[0])
	if err != nil {
		return err
	}
	st := a.store()
	body, meta, err := st.Load(id, pagecache.Options{})
	if err != nil {
		return err
	}
	fmt.Fprintln(a.out, header(meta, body))
	prev, ok := st.Prev(id)
	if !ok {
		fmt.Fprintln(a.out, "前の版がまだありません（読むのが初めて）。次に変更があったら差分が出せます")
		return nil
	}
	d := textdiff.Unified(splitLines(prev), splitLines(body), "前の版 "+notion.JST(meta.PrevEdited), "今 "+notion.JST(meta.Edited), *ctx)
	if d == "" {
		d = "差分なし"
	}
	fmt.Fprintln(a.out, d)
	return nil
}

func (a *app) cmdDBs(args []string) error {
	words, err := parse(newFlags("dbs"), args)
	if err != nil {
		return err
	}
	ix, err := a.ensureIndex()
	if err != nil {
		return err
	}
	for _, h := range ix.Find(index.FindOptions{Words: words, Kind: "db"}) {
		line := fmt.Sprintf("%s  %s  %s", h.ID, date(h.Edited), h.Title)
		if al := a.aliases.Of(h.ID); al != "" {
			line += "  (別名: " + al + ")"
		}
		fmt.Fprintln(a.out, line)
	}
	return nil
}

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(s string) error { *m = append(*m, s); return nil }

var whereRe = regexp.MustCompile(`^(.+?)(~|=)(.*)$`)

func (a *app) cmdDB(args []string) error {
	fs := newFlags("db")
	schema := fs.Bool("schema", false, "列の一覧と選択肢")
	title := fs.String("title", "", "タイトルに含む")
	var where multi
	fs.Var(&where, "where", "列名~文字列（含む）／列名=値（一致）。何回でも")
	since := fs.String("since", "", "この日付以降に更新")
	propsFlag := fs.String("props", "", "出す列（カンマ区切り）")
	filterJSON := fs.String("filter-json", "", "Notion API の filter をそのまま")
	limit := fs.Int("limit", 30, "最大件数")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fail("nn db <id>")
	}
	id, err := a.resolve(pos[0])
	if err != nil {
		return err
	}
	d, err := a.api().Get("databases/" + id)
	if err != nil {
		return err
	}
	props := notion.Map(d, "properties")
	names := sortedKeys(props)
	tname := ""
	for _, k := range names {
		if notion.Str(notion.Map(props, k), "type") == "title" {
			tname = k
		}
	}
	if *schema {
		fmt.Fprintf(a.out, "%s  [%s]\n", notion.Title(d), id)
		for _, k := range names {
			p := notion.Map(props, k)
			t := notion.Str(p, "type")
			var opts []string
			for _, o := range notion.Arr(notion.Map(p, t), "options") {
				om, _ := o.(map[string]any)
				opts = append(opts, notion.Str(om, "name"))
			}
			line := fmt.Sprintf("- %s (%s)", k, t)
			if len(opts) > 0 {
				line += ": " + strings.Join(opts[:min(len(opts), 20)], ", ")
			}
			fmt.Fprintln(a.out, line)
		}
		return nil
	}
	colList := strings.Join(names, ", ")
	var filters []any
	if *title != "" {
		filters = append(filters, notion.Obj{"property": tname, "title": notion.Obj{"contains": *title}})
	}
	if *since != "" {
		filters = append(filters, notion.Obj{"timestamp": "last_edited_time", "last_edited_time": notion.Obj{"on_or_after": *since}})
	}
	for _, w := range where {
		m := whereRe.FindStringSubmatch(w)
		if m == nil || props[m[1]] == nil {
			return fail("--where は 列名~文字列（含む）か 列名=値（一致）。列名: %s", colList)
		}
		k, op, val := m[1], m[2], m[3]
		t := notion.Str(notion.Map(props, k), "type")
		var cond notion.Obj
		switch t {
		case "title", "rich_text", "url", "email", "phone_number":
			if op == "~" {
				cond = notion.Obj{"contains": val}
			} else {
				cond = notion.Obj{"equals": val}
			}
		case "select", "status":
			cond = notion.Obj{"equals": val}
		case "multi_select", "relation", "people":
			cond = notion.Obj{"contains": val}
		case "checkbox":
			v := strings.ToLower(val)
			cond = notion.Obj{"equals": v == "1" || v == "true" || v == "yes" || v == "✓"}
		case "number":
			f, err := strconv.ParseFloat(val, 64)
			if err != nil {
				return fail("%s は数値の列です: %s", k, val)
			}
			cond = notion.Obj{"equals": f}
		case "date":
			cond = notion.Obj{"on_or_after": val}
		default:
			return fail("%s（%s）では絞り込めません。--filter-json を使ってください", k, t)
		}
		filters = append(filters, notion.Obj{"property": k, t: cond})
	}
	if *filterJSON != "" {
		var f any
		if err := json.Unmarshal([]byte(*filterJSON), &f); err != nil {
			return fail("--filter-json が JSON として読めません: %v", err)
		}
		filters = append(filters, f)
	}
	body := notion.Obj{"sorts": []any{notion.Obj{"timestamp": "last_edited_time", "direction": "descending"}}, "page_size": min(*limit, 100)}
	if len(filters) == 1 {
		body["filter"] = filters[0]
	} else if len(filters) > 1 {
		body["filter"] = notion.Obj{"and": filters}
	}
	var cols []string
	if *propsFlag != "" {
		for _, c := range strings.Split(*propsFlag, ",") {
			c = strings.TrimSpace(c)
			if props[c] == nil {
				return fail("列「%s」がありません。列名: %s", c, colList)
			}
			cols = append(cols, c)
		}
	}
	fmt.Fprintln(a.out, strings.Join(append([]string{"id", "更新", tname}, cols...), "\t"))
	n := 0
	clean := strings.NewReplacer("\t", " ", "\n", " ")
	return a.api().Paginate("databases/"+id+"/query", body, func(p notion.Obj) error {
		row := []string{notion.Str(p, "id"), date(notion.Str(p, "last_edited_time")), notion.Title(p)}
		for _, c := range cols {
			row = append(row, clean.Replace(notion.PropString(notion.Map(notion.Map(p, "properties"), c))))
		}
		fmt.Fprintln(a.out, strings.Join(row, "\t"))
		if n++; n >= *limit {
			fmt.Fprintf(a.out, "（%d件で打ち切り。--limit で増やせる）\n", *limit)
			return notion.Stop
		}
		return nil
	})
}

func (a *app) cmdAlias(args []string) error {
	pos, err := parse(newFlags("alias"), args)
	if err != nil {
		return err
	}
	switch len(pos) {
	case 0:
		ix, _ := index.Load(indexPath())
		for _, k := range a.aliases.Names() {
			t := ""
			if id, err := config.NormID(a.aliases[k]); err == nil && ix != nil && ix.Items[id] != nil {
				t = ix.Items[id].Title
			}
			fmt.Fprintf(a.out, "%s\t%s\t%s\n", k, a.aliases[k], t)
		}
		if len(a.aliases) == 0 {
			fmt.Fprintln(a.out, "別名なし。`nn alias 名前 <IDかURL>` で登録できます")
		}
	case 1:
		delete(a.aliases, pos[0])
		if _, err := a.aliases.Save(); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "%s を消しました\n", pos[0])
	case 2:
		id, err := config.NormID(pos[1])
		if err != nil {
			return err
		}
		a.aliases[pos[0]] = id
		if _, err := a.aliases.Save(); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "%s → %s\n", pos[0], id)
	default:
		return fail("nn alias [名前 [<id>]]")
	}
	return nil
}

func (a *app) cmdSync(args []string) error {
	fs := newFlags("sync")
	full := fs.Bool("full", false, "索引を作り直す（1分ほど）")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	ix, err := index.Load(indexPath())
	if err != nil {
		if !*full {
			return err
		}
		ix = index.New(indexPath()) // a broken index is rebuilt from scratch
	}
	n, err := ix.Sync(a.api(), *full, a.aliasIDs(), a.errOut)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.errOut, "同期しました: %d件取得 / 合計%d件\n", n, len(ix.Items))
	return nil
}

func (a *app) cmdAuth(args []string) error {
	if len(args) != 1 {
		return fail("nn auth login|status|logout")
	}
	switch args[0] {
	case "login":
		key, err := readKey()
		if err != nil {
			return err
		}
		ws, err := checkKey(key)
		if err != nil {
			return err
		}
		p, err := config.SaveKey(key)
		if err != nil {
			return err
		}
		fmt.Fprintf(a.errOut, "OK（ワークスペース: %s）\n保存しました: %s\n次は `nn sync --full` で索引を作ってください\n", ws, p)
	case "status":
		key, src, err := config.LoadKey()
		if errors.Is(err, config.ErrNoKey) {
			return fail("キーが登録されていません。`nn auth login` を実行してください")
		}
		if err != nil {
			return err
		}
		ws, err := checkKey(key)
		if err != nil {
			return err
		}
		fmt.Fprintf(a.out, "OK（ワークスペース: %s、キーの場所: %s）\n", ws, src)
	case "logout":
		p, err := config.DeleteKey()
		if err != nil {
			return err
		}
		fmt.Fprintf(a.out, "消しました: %s\n", p)
	default:
		return fail("nn auth login|status|logout")
	}
	return nil
}

// readKey reads the key without echoing it, or from a pipe.
func readKey() (string, error) {
	var key string
	if fd := int(os.Stdin.Fd()); term.IsTerminal(fd) {
		fmt.Fprint(os.Stderr, "Notion のインテグレーションキーを貼り付けてください（画面には出ません）: ")
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		key = string(b)
	} else {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return "", fail("標準入力からキーを読めませんでした")
		}
		key = line
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", fail("キーが空です")
	}
	return key, nil
}

// checkKey calls users/me and returns the workspace name.
func checkKey(key string) (string, error) {
	me, err := notion.New(key).Get("users/me")
	if err != nil {
		return "", err
	}
	ws := notion.Str(notion.Map(me, "bot"), "workspace_name")
	if ws == "" {
		ws = "(名前なし)"
	}
	return ws, nil
}

func (a *app) cmdVersion([]string) error {
	fmt.Fprintln(a.out, "nn", a.version)
	return nil
}
