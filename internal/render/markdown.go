package render

import (
	"errors"
	"sort"
	"strings"

	"github.com/hengin-eer/nutmeg-notion-connect/internal/notion"
)

// OmittedMark replaces a truncated subtree that could not be fetched or placed.
const OmittedMark = "[省略: 取れなかったブロック]"

// Content is a page's v2 markdown. Omitted counts the places that are still
// missing after trying to fill in truncated subtrees; such a page is partial.
type Content struct {
	Markdown string
	Omitted  int
}

// Markdown retrieves v2 content, replacing truncated subtrees in place. Requests
// are serial; memoization prevents repeated requests for shared synced blocks.
// Only a failure to fetch the page itself is an error. A subtree that cannot be
// fetched or placed is marked with OmittedMark and counted in Content.Omitted,
// so a huge page is still readable up to what Notion returned.
func Markdown(get func(string) (notion.Obj, error), id string) (Content, error) {
	type result struct {
		body    string
		omitted int
	}
	memo := map[string]result{}
	active := map[string]bool{}
	var load func(string) (result, error)
	load = func(id string) (result, error) {
		key := normalizeID(id)
		if r, ok := memo[key]; ok {
			return r, nil
		}
		if active[key] {
			return result{}, errors.New("Markdown API の不足ブロックが循環しています")
		}
		active[key] = true
		defer delete(active, key)
		obj, err := get("pages/" + id + "/markdown?markdown_version=v2")
		if err != nil {
			return result{}, err
		}
		body, ok := obj["markdown"].(string)
		if !ok {
			return result{}, errors.New("Markdown API の応答に本文がありません")
		}
		omitted := 0
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
			ids := notion.Arr(obj, "unknown_block_ids")
			if len(ids) == 0 {
				// Truncated, but Notion did not say where: keep what we have.
				omitted++
			}
			for _, v := range ids {
				bid, _ := v.(string)
				var matches []*node
				for _, n := range unknown {
					if bid != "" && normalizeID(urlID(n.attrs["url"])) == normalizeID(bid) {
						matches = append(matches, n)
					}
				}
				if len(matches) == 0 {
					// No place to put it, e.g. an unexpected URL form.
					omitted++
					continue
				}
				sub, err := load(bid)
				if err != nil {
					var apiErr *notion.Error
					if errors.As(err, &apiErr) && apiErr.Code == "object_not_found" {
						sub = result{body: "[見られないブロック]"}
						memo[normalizeID(bid)] = sub
					} else {
						sub = result{body: OmittedMark, omitted: 1}
					}
				}
				for _, n := range matches {
					lineStart := strings.LastIndex(body[:n.start], "\n") + 1
					indent := body[lineStart:n.start]
					if strings.TrimSpace(indent) != "" {
						indent = ""
					}
					changes = append(changes, replacement{n.start, n.end, strings.ReplaceAll(strings.TrimRight(sub.body, "\n"), "\n", "\n"+indent)})
					omitted += sub.omitted
				}
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
		r := result{body, omitted}
		memo[key] = r
		return r, nil
	}
	r, err := load(id)
	return Content{Markdown: r.body, Omitted: r.omitted}, err
}
func normalizeID(id string) string { return strings.ToLower(strings.ReplaceAll(id, "-", "")) }
