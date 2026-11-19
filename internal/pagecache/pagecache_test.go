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

// A partial page is shown with its count, refetched on the next read even if
// the page has not changed, and never kept as the previous version.
func TestPartialPageIsRefetched(t *testing.T) {
	dir := t.TempDir()
	markdownCalls := 0
	childFails := true
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	http.DefaultTransport = transport(func(r *http.Request) (*http.Response, error) {
		var o notion.Obj
		switch r.URL.Path {
		case "/v1/pages/page":
			o = notion.Obj{"last_edited_time": "2020-01-01T00:00:00Z"}
		case "/v1/pages/page/markdown":
			markdownCalls++
			o = notion.Obj{"markdown": "head\n<unknown url=\"https://example.test/page#child\" />\n", "truncated": true, "unknown_block_ids": []any{"child"}}
		case "/v1/pages/child/markdown":
			if childFails {
				return &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(`{"code":"validation_error"}`))}, nil
			}
			o = notion.Obj{"markdown": "child body"}
		default:
			t.Fatalf("unexpected endpoint %s", r.URL.Path)
		}
		b, _ := json.Marshal(o)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(b)))}, nil
	})
	store := Store{Dir: dir, Client: func() *notion.Client { return notion.New("synthetic") }}
	body, m, err := store.Load("page", Options{})
	if err != nil || m.Omitted != 1 || !strings.Contains(body, "[省略: 取れなかったブロック]") {
		t.Fatalf("partial: %q %+v %v", body, m, err)
	}
	// Offline reads still get the partial copy, flagged.
	if _, m, err := store.Load("page", Options{CachedOnly: true}); err != nil || m.Omitted != 1 || markdownCalls != 1 {
		t.Fatalf("cached partial: %+v %v calls=%d", m, err, markdownCalls)
	}
	childFails = false
	body, m, err = store.Load("page", Options{})
	if err != nil || m.Omitted != 0 || body != "head\nchild body\n" || markdownCalls != 2 {
		t.Fatalf("refetch: %q %+v %v calls=%d", body, m, err, markdownCalls)
	}
	if _, ok := store.Prev("page"); ok {
		t.Fatal("a partial copy must not become the previous version")
	}
}

// Prefetch uses the index's edit time instead of a page lookup, and the
// previous version stays the last one read, however many prefetches follow.
func TestPrefetchKeepsLastReadVersion(t *testing.T) {
	dir := t.TempDir()
	edited, body := "2020-01-01T00:00:00Z", "v1"
	pageCalls, markdownCalls := 0, 0
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	http.DefaultTransport = transport(func(r *http.Request) (*http.Response, error) {
		var o notion.Obj
		switch r.URL.Path {
		case "/v1/pages/page":
			pageCalls++
			o = notion.Obj{"last_edited_time": edited, "properties": notion.Obj{}}
		case "/v1/pages/page/markdown":
			markdownCalls++
			o = notion.Obj{"markdown": body}
		default:
			t.Fatalf("unexpected endpoint %s", r.URL.Path)
		}
		b, _ := json.Marshal(o)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(b)))}, nil
	})
	store := Store{Dir: dir, Client: func() *notion.Client { return notion.New("synthetic") }}
	prefetch := func(want bool) {
		t.Helper()
		ok, err := store.Prefetch("page", "Title", edited)
		if err != nil || ok != want {
			t.Fatalf("prefetch: ok=%v err=%v", ok, err)
		}
	}

	if _, _, err := store.Load("page", Options{}); err != nil { // read v1
		t.Fatal(err)
	}
	edited, body = "2020-01-02T00:00:00Z", "v2"
	prefetch(true)
	prefetch(false) // already current: no request
	edited, body = "2020-01-03T00:00:00Z", "v3"
	prefetch(true)
	if pageCalls != 1 || markdownCalls != 3 {
		t.Fatalf("prefetch must not look up the page: page=%d markdown=%d", pageCalls, markdownCalls)
	}
	if prev, ok := store.Prev("page"); !ok || prev != "v1\n" {
		t.Fatalf("previous must be the last read version: %q %v", prev, ok)
	}

	// Reading the prefetched copy costs only the update check and marks it read.
	got, m, err := store.Load("page", Options{})
	if err != nil || got != "v3\n" || m.Unseen || m.PrevEdited != "2020-01-01T00:00:00Z" || markdownCalls != 3 {
		t.Fatalf("read prefetched: %q %+v %v markdown=%d", got, m, err, markdownCalls)
	}
	edited, body = "2020-01-04T00:00:00Z", "v4"
	prefetch(true)
	if prev, _ := store.Prev("page"); prev != "v3\n" {
		t.Fatalf("after reading v3, previous must be v3: %q", prev)
	}
}
