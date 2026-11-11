package render

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hengin-eer/nutmeg-notion-connect/internal/notion"
)

// fixture maps a block ID to its children, as the API would return them.
const fixture = `{
 "page": [
  {"id":"h1","type":"heading_2","has_children":false,"heading_2":{"rich_text":[{"plain_text":"準備"}]}},
  {"id":"p1","type":"paragraph","has_children":false,"paragraph":{"rich_text":[{"plain_text":"詳しくは"},{"plain_text":"こちら","href":"https://example.com"}]}},
  {"id":"n1","type":"numbered_list_item","has_children":false,"numbered_list_item":{"rich_text":[{"plain_text":"一"}]}},
  {"id":"n2","type":"numbered_list_item","has_children":true,"numbered_list_item":{"rich_text":[{"plain_text":"二"}]}},
  {"id":"t1","type":"toggle","has_children":true,"toggle":{"rich_text":[{"plain_text":"開く"}]}},
  {"id":"c1","type":"callout","has_children":false,"callout":{"icon":{"emoji":"💡"},"rich_text":[{"plain_text":"注意"}]}},
  {"id":"tb","type":"table","has_children":true,"table":{}},
  {"id":"cp","type":"child_page","has_children":false,"child_page":{"title":"子"}},
  {"id":"h2","type":"heading_2","has_children":false,"heading_2":{"rich_text":[{"plain_text":"当日"}]}},
  {"id":"td","type":"to_do","has_children":false,"to_do":{"checked":true,"rich_text":[{"plain_text":"済"}]}}
 ],
 "n2": [
  {"id":"b1","type":"bulleted_list_item","has_children":false,"bulleted_list_item":{"rich_text":[{"plain_text":"下"}]}}
 ],
 "t1": [
  {"id":"p2","type":"paragraph","has_children":false,"paragraph":{"rich_text":[{"plain_text":"中身"}]}}
 ],
 "tb": [
  {"id":"r1","type":"table_row","table_row":{"cells":[[{"plain_text":"a"}],[{"plain_text":"b|c"}]]}},
  {"id":"r2","type":"table_row","table_row":{"cells":[[{"plain_text":"1"}],[{"plain_text":"2"}]]}}
 ]
}`

func fetcher(t *testing.T) Fetcher {
	var m map[string][]notion.Obj
	if err := json.Unmarshal([]byte(fixture), &m); err != nil {
		t.Fatal(err)
	}
	return func(id string) ([]notion.Obj, error) { return m[id], nil }
}

func TestPage(t *testing.T) {
	r := &Renderer{Fetch: fetcher(t)}
	got, err := r.Page("page")
	if err != nil {
		t.Fatal(err)
	}
	want := `## 準備
詳しくはこちら
1. 一
2. 二
  - 下
▸ 開く
  中身
> 💡 注意
| a | b\|c |
|---|---|
| 1 | 2 |
📄 子 (cp)
## 当日
- [x] 済
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestLinks(t *testing.T) {
	r := &Renderer{Fetch: fetcher(t), Links: true}
	got, _ := r.Page("page")
	if want := "詳しくはこちら（https://example.com）"; !strings.Contains(got, want) {
		t.Errorf("link not rendered: %s", got)
	}
}

func TestSection(t *testing.T) {
	r := &Renderer{Fetch: fetcher(t)}
	body, _ := r.Page("page")
	got, ok := Section(body, "当日")
	if !ok || got != "## 当日\n- [x] 済\n" {
		t.Errorf("section: %q", got)
	}
	hs := Headings(body)
	if len(hs) != 2 || hs[0].End != hs[1].Line {
		t.Errorf("headings: %+v", hs)
	}
}

func TestTidy(t *testing.T) {
	if got := Tidy([]string{"", "a  ", "", "", "b", ""}); got != "a\n\nb\n" {
		t.Errorf("tidy: %q", got)
	}
}
