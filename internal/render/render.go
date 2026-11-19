// Package render converts Notion-flavored Markdown to compact text for agents.
package render

import (
	"regexp"
	"strings"
)

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
