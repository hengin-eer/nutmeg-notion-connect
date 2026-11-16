package render

import (
	"errors"
	"sort"
	"strings"

	"github.com/hengin-eer/nutmeg-notion-connect/internal/notion"
)

// Markdown retrieves v2 content, replacing truncated subtrees in place. Requests
// are serial; memoization prevents repeated requests for shared synced blocks.
func Markdown(get func(string) (notion.Obj, error), id string) (string, error) {
	memo := map[string]string{}
	active := map[string]bool{}
	var load func(string) (string, error)
	load = func(id string) (string, error) {
		key := normalizeID(id)
		if body, ok := memo[key]; ok {
			return body, nil
		}
		if active[key] {
			return "", errors.New("Markdown API の不足ブロックが循環しています")
		}
		active[key] = true
		defer delete(active, key)
		obj, err := get("pages/" + id + "/markdown?markdown_version=v2")
		if err != nil {
			return "", err
		}
		body, ok := obj["markdown"].(string)
		if !ok {
			return "", errors.New("Markdown API の応答に本文がありません")
		}
		if notion.Bool(obj, "truncated") {
			type replacement struct {
				start, end int
				text       string
			}
			var changes []replacement
			var unknown []*node
			var walk func(*node)
			walk = func(n *node) {
				if n.tag == "unknown" {
					unknown = append(unknown, n)
				}
				for _, c := range n.children {
					walk(c)
				}
			}
			walk(parse(body))
			for _, v := range notion.Arr(obj, "unknown_block_ids") {
				bid, ok := v.(string)
				if !ok || bid == "" {
					return "", errors.New("Markdown API の不足ブロックIDが不正です")
				}
				var matches []*node
				for _, n := range unknown {
					if normalizeID(urlID(n.attrs["url"])) == normalizeID(bid) {
						matches = append(matches, n)
					}
				}
				if len(matches) == 0 {
					return "", errors.New("Markdown API の不足ブロックの挿入位置が見つかりません")
				}
				sub, err := load(bid)
				if err != nil {
					var apiErr *notion.Error
					if errors.As(err, &apiErr) && apiErr.Code == "object_not_found" {
						sub = "[見られないブロック]"
						memo[normalizeID(bid)] = sub
					} else {
						return "", err
					}
				}
				for _, n := range matches {
					lineStart := strings.LastIndex(body[:n.start], "\n") + 1
					indent := body[lineStart:n.start]
					if strings.TrimSpace(indent) != "" {
						indent = ""
					}
					changes = append(changes, replacement{n.start, n.end, strings.ReplaceAll(strings.TrimRight(sub, "\n"), "\n", "\n"+indent)})
				}
			}
			if len(changes) == 0 {
				return "", errors.New("Markdown API の本文が省略されていますが不足ブロックIDがありません")
			}
			sort.Slice(changes, func(i, j int) bool { return changes[i].start > changes[j].start })
			last := len(body)
			for _, c := range changes {
				if c.end > last {
					continue
				}
				body = body[:c.start] + c.text + body[c.end:]
				last = c.start
			}
		}
		memo[key] = body
		return body, nil
	}
	return load(id)
}
func normalizeID(id string) string { return strings.ToLower(strings.ReplaceAll(id, "-", "")) }
