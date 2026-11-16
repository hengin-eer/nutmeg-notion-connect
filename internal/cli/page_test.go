package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/hengin-eer/nutmeg-notion-connect/internal/config"
	"github.com/hengin-eer/nutmeg-notion-connect/internal/notion"
)

type pageTransport func(*http.Request) (*http.Response, error)

func (f pageTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPageCommandsWithMarkdownCache(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	const id = "00000000-0000-0000-0000-000000000001"
	raw := "## 準備\n[old](https://example.test/old)\n## 当日\n- [x] 確認\n"
	edited := "2020-01-01T00:00:00Z"
	calls := 0
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	http.DefaultTransport = pageTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		o := notion.Obj{"last_edited_time": edited}
		if strings.HasSuffix(r.URL.Path, "/markdown") {
			o = notion.Obj{"markdown": raw}
		}
		b, _ := json.Marshal(o)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(b))}, nil
	})
	var out bytes.Buffer
	a := app{out: &out, errOut: &out, aliases: config.Aliases{}, client: notion.New("synthetic")}
	run := func(fn func([]string) error, args ...string) string {
		t.Helper()
		out.Reset()
		if err := fn(args); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	if got := run(a.cmdRead, id, "--fresh", "--all"); !strings.Contains(got, "\nold\n") || strings.Contains(got, "https://") {
		t.Fatalf("read: %q", got)
	}
	if got := run(a.cmdRead, id, "--cached", "--links"); !strings.Contains(got, "[old](https://example.test/old)") {
		t.Fatalf("links: %q", got)
	}
	if got := run(a.cmdOutline, id, "--cached"); !strings.Contains(got, "## 準備") || !strings.Contains(got, "L3") {
		t.Fatalf("outline: %q", got)
	}
	if got := run(a.cmdRead, id, "--cached", "-s", "当日"); !strings.Contains(got, "- [x] 確認") || strings.Contains(got, "\nold\n") {
		t.Fatalf("section: %q", got)
	}
	if got := run(a.cmdGrep, id, "old", "--cached"); !strings.Contains(got, "L2 [準備] old") {
		t.Fatalf("grep: %q", got)
	}
	if calls != 2 {
		t.Fatalf("cached commands made extra requests: %d", calls)
	}
	raw = strings.ReplaceAll(raw, "old", "new")
	edited = "2020-01-02T00:00:00Z"
	if got := run(a.cmdDiff, id); !strings.Contains(got, "-old") || !strings.Contains(got, "+new") || strings.Contains(got, "https://") {
		t.Fatalf("diff: %q", got)
	}
	if calls != 4 {
		t.Fatalf("update requests: %d", calls)
	}
}
