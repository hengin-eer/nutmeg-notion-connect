package pagecache

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hengin-eer/nutmeg-notion-connect/internal/notion"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSharedCache(t *testing.T) {
	dir := t.TempDir()
	calls := 0
	markdownCalls := 0
	edited := "2020-01-01T00:00:00Z"
	raw := "## Heading\n[link](https://example.test)\n"
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	http.DefaultTransport = transport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "GET" || r.Header.Get("Notion-Version") != "2022-06-28" {
			t.Fatal("request contract")
		}
		var o notion.Obj
		switch r.URL.Path {
		case "/v1/pages/page":
			o = notion.Obj{"last_edited_time": edited}
		case "/v1/pages/page/markdown":
			markdownCalls++
			if r.URL.Query().Get("markdown_version") != "v2" {
				t.Fatal("expected v2")
			}
			o = notion.Obj{"markdown": raw}
		default:
			t.Fatalf("unexpected endpoint %s", r.URL.Path)
		}
		b, _ := json.Marshal(o)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(b)))}, nil
	})
	store := Store{Dir: dir, Client: func() *notion.Client { return notion.New("synthetic") }}
	// Legacy caches, including a links cache, must never be read or diffed.
	for p, b := range map[string]string{"page.md": "legacy", "page.json": `{"edited":"2020-01-01T00:00:00Z","fetched":9999999999}`, "page.prev.md": "legacy previous", "page.links.md": "legacy links", "page.links.json": "{}"} {
		if err := os.WriteFile(filepath.Join(dir, p), []byte(b), 0600); err != nil {
			t.Fatal(err)
		}
	}
	body, m, err := store.Load("page", Options{CachedOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if body != "## Heading\nlink\n" || m.Format != formatVersion || calls != 2 {
		t.Fatalf("migration: %q %+v calls=%d", body, m, calls)
	}
	if _, ok := store.Prev("page"); ok {
		t.Fatal("legacy previous must be invalidated")
	}
	body, _, err = store.Load("page", Options{Links: true, CachedOnly: true})
	if err != nil || body != raw || calls != 2 {
		t.Fatalf("offline links: %q %v", body, err)
	}
	body, _, err = store.Load("page", Options{})
	if err != nil || body != "## Heading\nlink\n" || calls != 3 || markdownCalls != 1 {
		t.Fatal("unchanged page should only check metadata")
	}
	body, _, err = store.Load("page", Options{Fresh: true})
	if err != nil || calls != 5 || markdownCalls != 2 {
		t.Fatal("fresh should fetch body")
	}
	edited = "2020-01-02T00:00:00Z"
	raw = "## Heading\n[changed](https://example.test/new)\n"
	_, m, err = store.Load("page", Options{})
	if err != nil || m.PrevEdited != "2020-01-01T00:00:00Z" {
		t.Fatalf("update: %v", err)
	}
	prev, ok := store.Prev("page")
	if !ok || prev != "## Heading\nlink\n" {
		t.Fatalf("prev: %q", prev)
	}
	saved, err := os.ReadFile(filepath.Join(dir, "page.md"))
	if err != nil || string(saved) != raw {
		t.Fatal("cache must retain URLs")
	}
}

func TestEditSettle(t *testing.T) {
	// A timestamp close to the fetch is not settled, even if unchanged.
	dir := t.TempDir()
	m := Meta{Format: formatVersion, Edited: "2020-01-01T00:00:00Z", Fetched: 1577836810}
	b, _ := json.Marshal(m)
	_ = os.WriteFile(filepath.Join(dir, "page.json"), b, 0600)
	_ = os.WriteFile(filepath.Join(dir, "page.md"), []byte("old\n"), 0600)
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	calls := 0
	http.DefaultTransport = transport(func(r *http.Request) (*http.Response, error) {
		calls++
		body := `{"last_edited_time":"2020-01-01T00:00:00Z"}`
		if strings.HasSuffix(r.URL.Path, "/markdown") {
			body = `{"markdown":"new"}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	s := Store{Dir: dir, Client: func() *notion.Client { return notion.New("synthetic") }}
	body, _, err := s.Load("page", Options{})
	if err != nil || body != "new\n" || calls != 2 {
		t.Fatalf("settle: %q %v calls=%d", body, err, calls)
	}
}
