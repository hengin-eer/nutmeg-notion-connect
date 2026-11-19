package render

import (
	"strings"
	"testing"
)

func TestSection(t *testing.T) {
	body := Squash("## 準備\n内容\n## 当日\n- [x] 済\n", false)
	got, ok := Section(body, "当日")
	if !ok || !strings.HasPrefix(got, "## 当日\n- [x] 済\n") {
		t.Fatalf("section: %q", got)
	}
	hs := Headings(body)
	if len(hs) != 2 || hs[0].End != hs[1].Line {
		t.Fatalf("headings: %+v", hs)
	}
}
func TestTidy(t *testing.T) {
	if got := Tidy([]string{"", "a  ", "", "", "b", ""}); got != "a\n\nb\n" {
		t.Fatalf("tidy: %q", got)
	}
}
