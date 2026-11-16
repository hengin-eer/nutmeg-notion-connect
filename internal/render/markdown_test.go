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
	got, err := Markdown(get, "root")
	if err != nil {
		t.Fatal(err)
	}
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
func TestMarkdownErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		obj  notion.Obj
		err  error
	}{
		{"cycle", notion.Obj{"markdown": `<unknown url="https://example.test/root" />`, "truncated": true, "unknown_block_ids": []any{"root"}}, nil},
		{"missing marker", notion.Obj{"markdown": "body", "truncated": true, "unknown_block_ids": []any{"missing"}}, nil},
		{"missing IDs", notion.Obj{"markdown": "body", "truncated": true}, nil},
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
func TestMarkdownDoesNotHideErrors(t *testing.T) {
	for _, code := range []string{"restricted_resource", "validation_error"} {
		_, err := Markdown(func(path string) (notion.Obj, error) {
			if strings.Contains(path, "pages/root/") {
				return notion.Obj{"markdown": `<unknown url="https://example.test/child" />`, "truncated": true, "unknown_block_ids": []any{"child"}}, nil
			}
			return nil, &notion.Error{Status: 403, Code: code}
		}, "root")
		if err == nil {
			t.Fatal("permission or API errors must propagate")
		}
	}
}
