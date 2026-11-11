package index

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hengin-eer/nutmeg-notion-connect/internal/notion"
)

// OverviewOptions scopes and sizes the overview.
type OverviewOptions struct {
	InDB  string // only items directly under this DB; "" for the whole workspace
	Top   int    // rows per section
	Days  int    // window for "recently active"
	Now   time.Time
	Label func(id string) string // alias name for an ID, or ""
}

var (
	// ASCII words of 3+ chars, or runs of 2+ Japanese characters.
	wordRe = regexp.MustCompile(`[A-Za-z][A-Za-z0-9]{2,}|[\p{Han}\p{Hiragana}\p{Katakana}ー々]{2,}`)
	// Generation prefixes such as 45th / 46th.
	genRe     = regexp.MustCompile(`(?i)(\d{1,3})(st|nd|rd|th)`)
	stopWords = map[string]bool{"page": true, "untitled": true, "the": true, "and": true, "for": true, "無題": true, "無題db": true}
)

type count struct {
	key string
	n   int
}

func topCounts(m map[string]int, n int) []count {
	out := make([]count, 0, len(m))
	for k, v := range m {
		out = append(out, count{k, v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].n != out[j].n {
			return out[i].n > out[j].n
		}
		return out[i].key < out[j].key
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// Overview summarizes the workspace from the index alone (no API calls), so an
// agent can learn its shape before searching.
func (ix *Index) Overview(o OverviewOptions) string {
	if o.Top <= 0 {
		o.Top = 10
	}
	if o.Days <= 0 {
		o.Days = 30
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	label := func(id string) string {
		t := "(不明)"
		if it := ix.Items[id]; it != nil {
			t = it.Title
		}
		if o.Label != nil {
			if a := o.Label(id); a != "" {
				t += " [" + a + "]"
			}
		}
		return t
	}

	var items []*Item
	pages, dbs := 0, 0
	for _, it := range ix.Items {
		if it.Archived || (o.InDB != "" && it.Parent != o.InDB) {
			continue
		}
		items = append(items, it)
		if it.Kind == "db" {
			dbs++
		} else {
			pages++
		}
	}

	var sb strings.Builder
	scope := "ワークスペース全体"
	if o.InDB != "" {
		scope = "DB「" + label(o.InDB) + "」の中"
	}
	synced := "未同期"
	if t, err := time.Parse(time.RFC3339, ix.SyncedAt); err == nil {
		synced = notion.JST(t.Format(time.RFC3339))
	}
	fmt.Fprintf(&sb, "%s：ページ %d / DB %d（索引の同期 %s）\n", scope, pages, dbs, synced)

	if o.InDB == "" {
		counts := ix.ChildCounts()
		var dbCounts, pageCounts = map[string]int{}, map[string]int{}
		for id, n := range counts {
			it := ix.Items[id]
			if it == nil || it.Archived {
				continue
			}
			if it.Kind == "db" {
				dbCounts[id] = n
			} else {
				pageCounts[id] = n
			}
		}
		sb.WriteString("\n## 中身の多いDB（件数 / 最終更新 / ID）\n")
		for _, c := range topCounts(dbCounts, o.Top) {
			edited := notion.JST(ix.Items[c.key].Edited)
			fmt.Fprintf(&sb, "%5d  %s  %s  %s\n", c.n, edited[:min(10, len(edited))], label(c.key), c.key)
		}
		sb.WriteString("\n## 子ページの多いページ（件数 / ID）\n")
		for _, c := range topCounts(pageCounts, o.Top) {
			fmt.Fprintf(&sb, "%5d  %s  %s\n", c.n, label(c.key), c.key)
		}
	}

	words := map[string]int{}
	spellings := map[string]map[string]int{} // lower-cased word → spelling → count
	gens := map[int]int{}
	for _, it := range items {
		seen := map[string]bool{}
		for _, w := range wordRe.FindAllString(it.Title, -1) {
			k := strings.ToLower(w)
			if stopWords[k] || seen[k] {
				continue
			}
			seen[k] = true
			words[k]++
			if spellings[k] == nil {
				spellings[k] = map[string]int{}
			}
			spellings[k][w]++
		}
		g := map[int]bool{}
		for _, m := range genRe.FindAllStringSubmatch(it.Title, -1) {
			n, _ := strconv.Atoi(m[1])
			if !g[n] {
				g[n] = true
				gens[n]++
			}
		}
	}
	sb.WriteString("\n## タイトルによく出る言葉（プロジェクト名・会議の種類など）\n")
	var ws []string
	for _, c := range topCounts(words, o.Top*3) {
		ws = append(ws, fmt.Sprintf("%s %d", topCounts(spellings[c.key], 1)[0].key, c.n))
	}
	sb.WriteString(strings.Join(ws, ", ") + "\n")

	if len(gens) > 0 {
		sb.WriteString("\n## 代の接頭辞（新しい順）\n")
		keys := make([]int, 0, len(gens))
		for k := range gens {
			keys = append(keys, k)
		}
		sort.Sort(sort.Reverse(sort.IntSlice(keys)))
		var gs []string
		for _, k := range keys[:min(len(keys), 6)] {
			gs = append(gs, fmt.Sprintf("%s %d", ordinal(k), gens[k]))
		}
		sb.WriteString(strings.Join(gs, ", ") + "\n")
	}

	since := o.Now.AddDate(0, 0, -o.Days).UTC().Format(time.RFC3339)
	active := map[string]int{}
	recent := 0
	for _, it := range items {
		if it.Edited < since {
			continue
		}
		recent++
		if it.Parent != "" && ix.Items[it.Parent] != nil {
			active[it.Parent]++
		}
	}
	if o.InDB != "" {
		fmt.Fprintf(&sb, "\n最近%d日に更新：%d件\n", o.Days, recent)
		return sb.String()
	}
	fmt.Fprintf(&sb, "\n## 最近%d日に更新された場所（%d件）\n", o.Days, recent)
	for _, c := range topCounts(active, o.Top) {
		fmt.Fprintf(&sb, "%5d  %s  %s\n", c.n, label(c.key), c.key)
	}
	return sb.String()
}

// ordinal writes 41 as "41st". Titles mix suffixes ("41th", "41rd"), so counts
// are merged by number and shown in one spelling.
func ordinal(n int) string {
	suffix := "th"
	if n%100 < 11 || n%100 > 13 {
		switch n % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}
	return strconv.Itoa(n) + suffix
}
