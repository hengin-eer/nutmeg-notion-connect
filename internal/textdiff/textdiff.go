// Package textdiff produces line-based unified diffs.
package textdiff

import (
	"fmt"
	"strings"
)

// Above this many cells the LCS table gets too large, so the changed middle is
// shown as one replace instead. Pages usually change locally, and the common
// prefix and suffix are trimmed first, so this is rarely hit.
const maxCells = 4_000_000

type op struct {
	kind byte // ' ', '-', '+'
	text string
}

func script(a, b []string) []op {
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	var ops []op
	for _, l := range a[:pre] {
		ops = append(ops, op{' ', l})
	}
	ma, mb := a[pre:len(a)-suf], b[pre:len(b)-suf]
	if len(ma)*len(mb) > maxCells {
		for _, l := range ma {
			ops = append(ops, op{'-', l})
		}
		for _, l := range mb {
			ops = append(ops, op{'+', l})
		}
	} else {
		ops = append(ops, lcs(ma, mb)...)
	}
	for _, l := range a[len(a)-suf:] {
		ops = append(ops, op{' ', l})
	}
	return ops
}

func lcs(a, b []string) []op {
	n, m := len(a), len(b)
	t := make([][]int32, n+1)
	for i := range t {
		t[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				t[i][j] = t[i+1][j+1] + 1
			} else {
				t[i][j] = max(t[i+1][j], t[i][j+1])
			}
		}
	}
	var ops []op
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, op{' ', a[i]})
			i++
			j++
		case t[i+1][j] >= t[i][j+1]:
			ops = append(ops, op{'-', a[i]})
			i++
		default:
			ops = append(ops, op{'+', b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, op{'-', a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, op{'+', b[j]})
	}
	return ops
}

// Unified returns a unified diff of a and b with ctx lines of context,
// or "" when they are equal.
func Unified(a, b []string, fromLabel, toLabel string, ctx int) string {
	ops := script(a, b)
	// Mark which ops fall inside a hunk: changes plus ctx lines around them.
	keep := make([]bool, len(ops))
	changed := false
	for i, o := range ops {
		if o.kind == ' ' {
			continue
		}
		changed = true
		for k := max(0, i-ctx); k <= min(len(ops)-1, i+ctx); k++ {
			keep[k] = true
		}
	}
	if !changed {
		return ""
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "--- %s\n+++ %s\n", fromLabel, toLabel)
	ai, bi := 1, 1 // 1-based line numbers before op i
	for i := 0; i < len(ops); {
		if !keep[i] {
			if ops[i].kind != '+' {
				ai++
			}
			if ops[i].kind != '-' {
				bi++
			}
			i++
			continue
		}
		j := i
		for j < len(ops) && keep[j] {
			j++
		}
		var na, nb int
		var body strings.Builder
		for _, o := range ops[i:j] {
			body.WriteString(string(o.kind) + o.text + "\n")
			if o.kind != '+' {
				na++
			}
			if o.kind != '-' {
				nb++
			}
		}
		fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@\n", ai, na, bi, nb)
		sb.WriteString(body.String())
		ai += na
		bi += nb
		i = j
	}
	return strings.TrimSuffix(sb.String(), "\n")
}
