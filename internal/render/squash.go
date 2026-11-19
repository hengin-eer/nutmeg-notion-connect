package render

import (
	"html"
	"regexp"
	"strconv"
	"strings"
)

// node is a small syntax tree, rather than an HTML DOM: Markdown code and
// escaped punctuation must remain opaque to the tag parser.
type node struct {
	start, end int
	tag, text  string
	attrs      map[string]string
	children   []*node
	raw        bool
}

var attrsRE = regexp.MustCompile(`([\w-]+)\s*=\s*(?:"([^"]*)"|'([^']*)')`)
var tags = strings.Fields("b strong i em u s del span code p br toggle callout ul ol li page database mention date image file pdf video audio embed bookmark synced-block synced-block-reference columns column quote table thead tbody tr td th colgroup col table-of-contents unknown h1 h2 h3 h4")

func knownTag(s string) bool {
	for _, t := range tags {
		if s == t {
			return true
		}
	}
	return false
}

// parse preserves unknown syntax verbatim, including future Notion extensions.
func parse(s string) *node { return parseSyntax(s, true) }
func parseSyntax(s string, tagsEnabled bool) *node {
	root := &node{}
	stack := []*node{root}
	add := func(n *node) {
		p := stack[len(stack)-1]
		if n.tag == "" && !n.raw && len(p.children) > 0 {
			last := p.children[len(p.children)-1]
			if last.tag == "" && !last.raw {
				last.text += n.text
				return
			}
		}
		p.children = append(p.children, n)
	}
	for i := 0; i < len(s); {
		start := i
		if s[i] == '\\' && i+1 < len(s) {
			i += 2
			add(&node{text: s[start:i]})
			continue
		}
		// Consume links as a unit so code and styled spans in labels cannot
		// split the URL away from its label.
		labelStart := i
		image := s[i] == '!' && i+1 < len(s) && s[i+1] == '['
		if image {
			labelStart++
		}
		if s[labelStart] == '[' {
			labelEnd := balanced(s, labelStart, '[', ']')
			if labelEnd >= 0 && labelEnd+1 < len(s) && s[labelEnd+1] == '(' {
				end := balanced(s, labelEnd+1, '(', ')')
				if end >= 0 {
					if tagsEnabled {
						n := &node{tag: "markdown-link", attrs: map[string]string{"source": s[labelEnd+2 : end]}, children: parse(s[labelStart+1 : labelEnd]).children}
						if image {
							n.attrs["image"] = "true"
						}
						add(n)
					} else {
						add(&node{text: s[i : end+1]})
					}
					i = end + 1
					continue
				}
			}
		}
		// Both inline code and fenced blocks are copied byte for byte.
		if s[i] == '`' || (s[i] == '~' && (i == 0 || s[i-1] == '\n' || s[i-1] == ' ' || s[i-1] == '\t')) {
			ch := s[i]
			j := i
			for j < len(s) && s[j] == ch {
				j++
			}
			run := j - i
			lineStart := strings.LastIndex(s[:i], "\n") + 1
			fence := run >= 3 && strings.TrimSpace(s[lineStart:i]) == ""
			end := -1
			if fence {
				for k := j; k < len(s); {
					n := strings.IndexByte(s[k:], '\n')
					if n < 0 {
						break
					}
					k += n + 1
					e := strings.IndexByte(s[k:], '\n')
					if e < 0 {
						e = len(s) - k
					}
					line := strings.TrimSpace(s[k : k+e])
					if len(line) >= run && strings.Trim(line, string(ch)) == "" {
						end = k + e
						break
					}
				}
				if end < 0 {
					end = len(s)
				}
			} else if ch == '`' {
				for k := j; k < len(s); {
					n := strings.IndexByte(s[k:], '`')
					if n < 0 {
						break
					}
					k += n
					e := k
					for e < len(s) && s[e] == '`' {
						e++
					}
					if e-k == run {
						end = e
						break
					}
					k = e
				}
			}
			if end >= 0 {
				add(&node{text: s[i:end], raw: true})
				i = end
				continue
			}
		}
		if tagsEnabled && s[i] == '<' {
			j := i + 1
			var quote byte
			for ; j < len(s); j++ {
				c := s[j]
				if quote != 0 {
					if c == quote {
						quote = 0
					}
				} else if c == '"' || c == '\'' {
					quote = c
				} else if c == '>' {
					break
				}
			}
			if j < len(s) {
				inside := strings.TrimSpace(s[i+1 : j])
				closing := strings.HasPrefix(inside, "/")
				inside = strings.TrimPrefix(inside, "/")
				name := strings.Fields(inside)
				tag := ""
				if len(name) > 0 {
					tag = strings.TrimSuffix(name[0], "/")
				}
				if knownTag(tag) {
					if closing {
						if len(stack) > 1 && stack[len(stack)-1].tag == tag {
							stack = stack[:len(stack)-1]
							i = j + 1
							continue
						}
					} else {
						a := map[string]string{}
						for _, m := range attrsRE.FindAllStringSubmatch(inside, -1) {
							a[m[1]] = html.UnescapeString(m[2] + m[3])
						}
						n := &node{tag: tag, attrs: a, start: i, end: j + 1}
						add(n)
						if !strings.HasSuffix(inside, "/") && tag != "br" && tag != "col" {
							stack = append(stack, n)
						}
						i = j + 1
						continue
					}
				}
			}
		}
		i++
		for i < len(s) && !strings.ContainsRune("<`~\\[!", rune(s[i])) {
			i++
		}
		add(&node{text: s[start:i]})
	}
	return root
}

// Squash converts Notion-flavored Markdown v2 to nn's compact representation.
// Code is opaque. URLs remain only when links is true; signed media never does.
func Squash(md string, links bool) string {
	return strings.TrimRight(compact(renderNode(parse(md), links)), "\n") + "\n"
}

func renderNode(n *node, links bool) string {
	if n.tag == "" && n.children == nil {
		if n.raw {
			return n.text
		}
		return inline(n.text, links)
	}
	var b strings.Builder
	for _, c := range n.children {
		b.WriteString(renderNode(c, links && n.tag != "markdown-link"))
	}
	s := b.String()
	t := strings.TrimSpace(s)
	switch n.tag {
	case "markdown-link":
		if n.attrs["image"] == "true" {
			if s == "" {
				s = "[image]"
			} else {
				s = "[image: " + s + "]"
			}
		}
		if links && !signed(n.attrs["source"]) {
			return "[" + escapeLabel(s) + "](" + n.attrs["source"] + ")"
		}
		return s

	case "b", "strong", "i", "em", "u", "s", "del", "span":
		return s
	case "code":
		return "`" + s + "`"
	case "p":
		if t == "" {
			return ""
		}
		return t + "\n"
	case "br":
		return " "
	case "toggle":
		if h := n.attrs["heading"]; len(h) == 2 && h[0] == 'h' && h[1] >= '1' && h[1] <= '4' {
			// Toggle-heading content belongs to the section, so it stays unindented.
			return strings.Repeat("#", int(h[1]-'0')) + " " + t + "\n"
		}
		return container("▸ ", t)
	case "callout":
		prefix := "> "
		if icon := n.attrs["icon"]; icon != "" {
			prefix += icon + " "
		}
		return container(prefix, t)
	case "quote":
		return container("> ", t)
	case "ul", "ol":
		var out strings.Builder
		num := 0
		for _, c := range n.children {
			if c.tag != "li" {
				if strings.TrimSpace(renderNode(c, links)) != "" {
					out.WriteString(renderNode(c, links))
				}
				continue
			}
			num++
			prefix := "- "
			if n.tag == "ol" {
				prefix = strconv.Itoa(num) + ". "
			}
			v := strings.TrimSpace(renderNode(c, links))
			out.WriteString(prefix + strings.ReplaceAll(v, "\n", "\n  ") + "\n")
		}
		return out.String()
	case "li":
		var out strings.Builder
		for _, c := range n.children {
			v := renderNode(c, links)
			if c.tag == "ul" || c.tag == "ol" {
				out.WriteString("\n" + strings.TrimSpace(v))
			} else {
				out.WriteString(v)
			}
		}
		return strings.TrimSpace(out.String())
	case "page", "database":
		mark := "📄 "
		if n.tag == "database" {
			mark = "🗃 "
		}
		return mark + t + " (" + urlID(n.attrs["url"]) + ")\n"
	case "mention":
		u := n.attrs["url"]
		if strings.HasPrefix(u, "user://") {
			id := strings.TrimPrefix(u, "user://")
			return id[:min(8, len(id))]
		}
		if t != "" {
			return t
		}
		return "→" + urlID(u)
	case "date":
		return n.attrs["start"]
	case "image", "file", "pdf", "video", "audio", "embed", "bookmark":
		label := "[" + n.tag
		if t != "" {
			label += ": " + t
		}
		label += "]"
		u := n.attrs["source"]
		if links && u != "" && !signed(u) {
			return "[" + label + "](" + u + ")"
		}
		return label
	case "table":
		return renderTable(n, links)
	case "colgroup", "col", "table-of-contents":
		return ""
	case "unknown":
		return "[" + fallback(n.attrs["alt"], "見られないブロック") + "]"
	case "h1", "h2", "h3", "h4":
		return strings.Repeat("#", int(n.tag[1]-'0')) + " " + t + "\n"
	default:
		return s
	}
}

// container puts the title after prefix and indents the children beneath it,
// so headings inside toggles and callouts are not taken for page headings.
func container(prefix, t string) string {
	lines := strings.Split(t, "\n")
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "" {
			lines[i] = "  " + lines[i]
		}
	}
	return prefix + strings.Join(lines, "\n") + "\n"
}

func fallback(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
func signed(u string) bool {
	s := strings.ToLower(u)
	return strings.Contains(s, "prod-files-secure") || strings.Contains(s, "x-amz-")
}
func urlID(s string) string {
	if i := strings.LastIndex(s, "#"); i >= 0 {
		s = s[i+1:]
	} else if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	return s
}

func renderTable(n *node, links bool) string {
	var out strings.Builder
	first := true
	var walk func(*node)
	walk = func(n *node) {
		if n.tag == "tr" {
			var cells []string
			for _, c := range n.children {
				if c.tag == "td" || c.tag == "th" {
					s := strings.TrimSpace(renderNode(c, links))
					s = strings.ReplaceAll(s, "\n", " ")
					s = strings.ReplaceAll(s, "|", `\|`)
					cells = append(cells, s)
				}
			}
			if len(cells) > 0 {
				out.WriteString("| " + strings.Join(cells, " | ") + " |\n")
				if first {
					out.WriteString("|" + strings.Repeat("---|", len(cells)) + "\n")
					first = false
				}
			}
			return
		}
		for _, c := range n.children {
			walk(c)
		}
	}
	walk(n)
	return out.String()
}

// inline parses balanced Markdown links (including parentheses in URLs) before
// unescaping text. Cache links stay in Markdown so display can remove them later.
func inline(s string, links bool) string { return inlineMode(s, links, true) }
func inlineMode(s string, links, decode bool) string {
	var out strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '\\' && i+1 < len(s) && strings.ContainsRune(`!"#$%&'()*+,-./:;<=>?@[\]^_`+"`"+`{|}~`, rune(s[i+1])) {
			// Retain syntax-significant escapes in the shared Markdown cache.
			// Projection removes them without treating the literal as a link.
			critical := strings.ContainsRune("[]()`\\", rune(s[i+1]))
			if (decode && links && critical) || (!decode && !critical) {
				out.WriteByte(s[i])
			}
			out.WriteByte(s[i+1])
			i += 2
			continue
		}
		start := i
		image := s[i] == '!' && i+1 < len(s) && s[i+1] == '['
		if image {
			i++
		}
		if s[i] == '[' {
			end := balanced(s, i, '[', ']')
			if end >= 0 && end+1 < len(s) && s[end+1] == '(' {
				close := balanced(s, end+1, '(', ')')
				if close >= 0 {
					label := inlineMode(s[i+1:end], false, true)
					url := s[end+2 : close]
					if image {
						if label == "" {
							label = "[image]"
						} else {
							label = "[image: " + label + "]"
						}
					}
					if links && !signed(url) {
						out.WriteString("[" + escapeLabel(label) + "](" + url + ")")
					} else {
						out.WriteString(label)
					}
					i = close + 1
					continue
				}
			}
		}
		i = start
		out.WriteByte(s[i])
		i++
	}
	if decode {
		return html.UnescapeString(out.String())
	}
	return out.String()
}
func escapeLabel(s string) string {
	return strings.NewReplacer(`\`, `\\`, "[", `\[`, "]", `\]`).Replace(s)
}
func balanced(s string, start int, open, close byte) int {
	depth := 0
	for i := start; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if s[i] == open {
			depth++
		}
		if s[i] == close {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// WithoutLinks operates on compact cached text without reparsing literal tags
// that were escaped in the source. Code remains opaque.
func WithoutLinks(body string) string {
	return transformText(body, func(s string) string { return inlineMode(s, false, false) })
}
func transformText(s string, fn func(string) string) string {
	// Reuse code recognition without interpreting escaped or literal tag syntax.
	root := parseSyntax(s, false)
	var b strings.Builder
	for _, n := range root.children {
		if n.raw {
			b.WriteString(n.text)
		} else {
			b.WriteString(fn(n.text))
		}
	}
	return b.String()
}
func compact(s string) string {
	// Protect complete code tokens while removing blank lines in the surrounding text.
	marker := "\x00code"
	for strings.Contains(s, marker) {
		marker += "x"
	}
	root := parseSyntax(s, false)
	var b strings.Builder
	var codes []string
	for _, n := range root.children {
		if n.raw {
			b.WriteString(marker + strconv.Itoa(len(codes)) + "\x00")
			codes = append(codes, n.text)
		} else {
			b.WriteString(n.text)
		}
	}
	var lines []string
	for _, line := range strings.Split(b.String(), "\n") {
		line = strings.TrimRight(line, " \t\r")
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	out := strings.Join(lines, "\n")
	for i, code := range codes {
		out = strings.ReplaceAll(out, marker+strconv.Itoa(i)+"\x00", code)
	}
	return out
}
