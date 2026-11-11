package notion

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestRequestStats(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "off", true: "on"}[enabled], func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "stats")
			v := ""
			if enabled {
				v = p
			}
			t.Setenv("NN_STATS_FILE", v)
			c := New("secret_do_not_log")
			calls := 0
			c.http = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				status := 200
				body := `{}`
				h := http.Header{}
				if calls == 1 {
					status = 429
					h.Set("Retry-After", "0.001")
				}
				return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			if _, err := c.Get("search?private=hidden"); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(p)
			if enabled {
				if err != nil || string(b) != "request\nrequest\n" {
					t.Fatalf("stats=%q err=%v", b, err)
				}
			} else if !os.IsNotExist(err) {
				t.Fatal("unexpected stats file")
			}
		})
	}
}
func TestStatsFailure(t *testing.T) {
	t.Setenv("NN_STATS_FILE", t.TempDir())
	c := New("key")
	c.http = &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("request sent despite stats failure")
		return nil, nil
	})}
	if _, err := c.Get("search"); err == nil {
		t.Fatal("expected stats error")
	}
}
