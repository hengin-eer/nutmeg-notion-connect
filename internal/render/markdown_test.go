package render

import (
	"errors"
	"strings"
	"testing"

	"github.com/hengin-eer/nutmeg-notion-connect/internal/notion"
)

func TestMarkdownExpansion(t *testing.T) {
	const a = "00000000-0000-0000-0000-000000000001"
	const b = "00000000-0000-0000-0000-000000000002"
	const hidden = "00000000-0000-0000-0000-000000000003"
	marker := func(id string) string {
		return `<unknown alt="block" url="https://example.test/root#` + strings.ReplaceAll(id, "-", "") + `" />`
	}
	data := map[string]notion.Obj{
		"root": {"markdown": "before\n  " + marker(a) + "\nafter\n" + marker(a) + "\n" + marker(hidden), "truncated": true, "unknown_block_ids": []any{a, hidden}},
		a:      {"markdown": "## A\n" + marker(b), "truncated": true, "unknown_block_ids": []any{b}},
		b:      {"markdown": "body\n\n`" + marker(hidden) + "`\n"},
	}
	calls := map[string]int{}
	get := func(path string) (notion.Obj, error) {
		if !strings.HasSuffix(path, "/markdown?markdown_version=v2") {
			t.Fatalf("wrong endpoint: %s", path)
		}
		id := strings.TrimSuffix(strings.TrimPrefix(path, "pages/"), "/markdown?markdown_version=v2")
		calls[id]++
		if id == hidden {
			return nil, &notion.Error{Status: 404, Code: "object_not_found"}
		}
		return data[id], nil
	}
	c, err := Markdown(get, "root")
	if err != nil {
		t.Fatal(err)
	}
	if c.Omitted != 0 {
		t.Errorf("omitted=%d", c.Omitted)
	}
	got := c.Markdown
	want := "before\n  ## A\n  body\n  \n  `" + marker(hidden) + "`\nafter\n## A\nbody\n\n`" + marker(hidden) + "`\n[見られないブロック]"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	for _, id := range []string{"root", a, b, hidden} {
		if calls[id] != 1 {
			t.Errorf("calls[%s]=%d", id, calls[id])
		}
	}
}
// Only the page itself is required; anything below it degrades to a partial page.
func TestMarkdownErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		obj  notion.Obj
		err  error
	}{
		{"missing body", notion.Obj{}, nil},
		{"network", nil, errors.New("offline")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Markdown(func(string) (notion.Obj, error) { return tc.obj, tc.err }, "root"); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestMarkdownPartial(t *testing.T) {
	child := `<unknown alt="block" url="https://example.test/root#child" />`
	for _, tc := range []struct {
		name     string
		root     notion.Obj
		childErr error
		want     string
	}{
		{"cycle", notion.Obj{"markdown": "head\n" + `<unknown url="https://example.test/root" />`, "truncated": true, "unknown_block_ids": []any{"root"}}, nil, "head\n" + OmittedMark},
		{"missing marker", notion.Obj{"markdown": "head", "truncated": true, "unknown_block_ids": []any{"missing"}}, nil, "head"},
		{"unexpected URL form", notion.Obj{"markdown": "head\n" + `<unknown url="https://example.test/root#child?pvs=1" />`, "truncated": true, "unknown_block_ids": []any{"child"}}, nil, "head\n" + `<unknown url="https://example.test/root#child?pvs=1" />`},
		{"missing IDs", notion.Obj{"markdown": "head\n" + child, "truncated": true}, nil, "head\n" + child},
		{"rate limited", notion.Obj{"markdown": "head\n" + child + "\ntail", "truncated": true, "unknown_block_ids": []any{"child"}}, errors.New("再試行の上限に達しました"), "head\n" + OmittedMark + "\ntail"},
		{"restricted", notion.Obj{"markdown": "head\n" + child, "truncated": true, "unknown_block_ids": []any{"child"}}, &notion.Error{Status: 403, Code: "restricted_resource"}, "head\n" + OmittedMark},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := Markdown(func(path string) (notion.Obj, error) {
				if strings.Contains(path, "pages/root/") {
					return tc.root, nil
				}
				return nil, tc.childErr
			}, "root")
			if err != nil {
				t.Fatalf("a partial page must still be returned: %v", err)
			}
			if c.Omitted != 1 {
				t.Errorf("omitted=%d", c.Omitted)
			}
			if c.Markdown != tc.want {
				t.Errorf("got %q want %q", c.Markdown, tc.want)
			}
		})
	}
}
