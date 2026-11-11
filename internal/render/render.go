// Package render turns Notion blocks into compact markdown for agents:
// no type prefixes, toggles as ▸, callouts as >, URLs only on request.
package render

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/hengin-eer/nutmeg-notion-connect/internal/notion"
)

// Fetcher returns the child blocks of a block or page.
type Fetcher func(id string) ([]notion.Obj, error)

type Renderer struct {
	Fetch Fetcher
	Links bool
}

// Page renders a whole page as tidy markdown.
func (r *Renderer) Page(id string) (string, error) {
	lines, err := r.blocks(id, 0)
	if err != nil {
		return "", err
	}
	return Tidy(lines), nil
}

func (r *Renderer) blocks(id string, depth int) ([]string, error) {
	children, err := r.Fetch(id)
	if err != nil {
		return nil, err
	}
	var lines []string
	num := 0
	for _, b := range children {
		t := notion.Str(b, "type")
		v := notion.Typed(b)
		ind := strings.Repeat("  ", depth)
		text := notion.RichText(notion.Arr(v, "rich_text"), r.Links)
		kidsDepth := depth + 1
		line := ""
		if t == "numbered_list_item" {
			num++
		} else {
			num = 0
		}

		switch t {
		case "paragraph":
			line = text
		case "heading_1", "heading_2", "heading_3":
			line = strings.Repeat("#", int(t[len(t)-1]-'0')) + " " + text
			kidsDepth = depth // toggle-heading content is not indented
		case "bulleted_list_item":
			line = "- " + text
		case "numbered_list_item":
			line = strconv.Itoa(num) + ". " + text
		case "to_do":
			if notion.Bool(v, "checked") {
				line = "- [x] " + text
			} else {
				line = "- [ ] " + text
			}
		case "toggle":
			line = "▸ " + text
		case "quote":
			line = "> " + text
		case "callout":
			icon := notion.Str(notion.Map(v, "icon"), "emoji")
			if icon != "" {
				icon += " "
			}
			line = "> " + icon + text
		case "code":
			lines = append(lines, ind+"```"+notion.Str(v, "language"))
			if code := strings.TrimSuffix(text, "\n"); code != "" {
				for _, l := range strings.Split(code, "\n") {
					lines = append(lines, ind+l)
				}
			}
			lines = append(lines, ind+"```")
			continue
		case "equation":
			line = "$" + notion.Str(v, "expression") + "$"
		case "divider":
			line = "---"
		case "child_page":
			lines = append(lines, ind+"📄 "+notion.Str(v, "title")+" ("+notion.Str(b, "id")+")")
			continue
		case "child_database":
			lines = append(lines, ind+"🗃 "+notion.Str(v, "title")+" ("+notion.Str(b, "id")+")")
			continue
		case "link_to_page":
			lines = append(lines, ind+"→ "+notion.Str(v, notion.Str(v, "type")))
			continue
		case "bookmark", "embed", "link_preview":
			line = "[" + t + "] " + notion.Str(v, "url")
			if c := notion.RichText(notion.Arr(v, "caption"), false); c != "" {
				line += " " + c
			}
		case "image", "file", "pdf", "video", "audio":
			c := notion.RichText(notion.Arr(v, "caption"), false)
			if c == "" {
				c = notion.Str(v, "name")
			}
			line = "[" + t
			if c != "" {
				line += ": " + c
			}
			line += "]"
			url := notion.Str(notion.Map(v, "external"), "url")
			if url == "" {
				url = notion.Str(notion.Map(v, "file"), "url")
			}
			if r.Links && url != "" {
				line += " " + url
			}
		case "table":
			rows, err := r.Fetch(notion.Str(b, "id"))
			if err != nil {
				return nil, err
			}
			first := true
			for _, row := range rows {
				if notion.Str(row, "type") != "table_row" {
					continue
				}
				cells := notion.Arr(notion.Typed(row), "cells")
				out := make([]string, len(cells))
				for i, c := range cells {
					arr, _ := c.([]any)
					s := notion.RichText(arr, r.Links)
					out[i] = strings.NewReplacer("|", `\|`, "\n", " ").Replace(s)
				}
				lines = append(lines, ind+"| "+strings.Join(out, " | ")+" |")
				if first {
					lines = append(lines, ind+"|"+strings.Repeat("---|", len(cells)))
					first = false
				}
			}
			continue
		case "column_list", "column":
			sub, err := r.blocks(notion.Str(b, "id"), depth)
			if err != nil {
				return nil, err
			}
			lines = append(lines, sub...)
			continue
		case "synced_block":
			src := notion.Str(notion.Map(v, "synced_from"), "block_id")
			if src == "" {
				src = notion.Str(b, "id")
			}
			sub, err := r.blocks(src, depth)
			if err != nil {
				return nil, err
			}
			lines = append(lines, sub...)
			continue
		case "table_of_contents", "breadcrumb":
			continue
		default:
			line = "[" + t + "]"
		}

		for j, l := range strings.Split(line, "\n") {
			if j > 0 {
				l = "  " + l
			}
			lines = append(lines, ind+l)
		}
		if notion.Bool(b, "has_children") {
			sub, err := r.blocks(notion.Str(b, "id"), kidsDepth)
			if err != nil {
				return nil, err
			}
			lines = append(lines, sub...)
		}
	}
	return lines, nil
}

// Tidy trims trailing spaces and collapses runs of blank lines.
func Tidy(lines []string) string {
	var out []string
	for _, l := range lines {
		l = strings.TrimRight(l, " \t")
		if l == "" && (len(out) == 0 || out[len(out)-1] == "") {
			continue
		}
		out = append(out, l)
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n") + "\n"
}

// Heading is a markdown heading found in a rendered page.
type Heading struct {
	Line  int // 1-based
	Level int
	Text  string
	End   int // first line after this section (1-based, exclusive)
}

var headingRe = regexp.MustCompile(`^(#{1,3}) (.*)`)

// Headings lists top-level headings (toggles nested under lists are indented and skipped).
func Headings(body string) []Heading {
	lines := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
	var hs []Heading
	for i, l := range lines {
		if m := headingRe.FindStringSubmatch(l); m != nil {
			hs = append(hs, Heading{Line: i + 1, Level: len(m[1]), Text: m[2]})
		}
	}
	for k := range hs {
		hs[k].End = len(lines) + 1
		for _, h := range hs[k+1:] {
			if h.Level <= hs[k].Level {
				hs[k].End = h.Line
				break
			}
		}
	}
	return hs
}

// Section returns the first section whose heading contains query (case-insensitive).
func Section(body, query string) (string, bool) {
	lines := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
	q := strings.ToLower(query)
	for _, h := range Headings(body) {
		if strings.Contains(strings.ToLower(h.Text), q) {
			return strings.Join(lines[h.Line-1:h.End-1], "\n") + "\n", true
		}
	}
	return "", false
}
