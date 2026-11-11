package notion

import (
	"strconv"
	"strings"
	"time"
)

// Obj is a decoded Notion JSON object. Notion has many block and property
// shapes, so nn reads only the fields it needs through the helpers below.
type Obj = map[string]any

func Str(o Obj, k string) string { v, _ := o[k].(string); return v }
func Map(o Obj, k string) Obj    { v, _ := o[k].(map[string]any); return v }
func Arr(o Obj, k string) []any  { v, _ := o[k].([]any); return v }
func Bool(o Obj, k string) bool  { v, _ := o[k].(bool); return v }

// Typed returns o[o["type"]], the payload of a typed object such as a block.
func Typed(o Obj) Obj { return Map(o, Str(o, "type")) }

var jst = time.FixedZone("JST", 9*60*60)

// JST formats an ISO timestamp as "2006-01-02 15:04" in Japan time.
func JST(iso string) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return iso
	}
	return t.In(jst).Format("2006-01-02 15:04")
}

// RichText joins plain text. With links, URLs follow the text in （）.
func RichText(arr []any, links bool) string {
	var b strings.Builder
	for _, x := range arr {
		o, _ := x.(map[string]any)
		b.WriteString(Str(o, "plain_text"))
		if href := Str(o, "href"); links && href != "" && Str(o, "type") != "mention" {
			b.WriteString("（" + href + "）")
		}
	}
	return b.String()
}

// Title returns the title of a page or database.
func Title(o Obj) string {
	if Str(o, "object") == "database" {
		if t := RichText(Arr(o, "title"), false); t != "" {
			return t
		}
		return "(無題DB)"
	}
	for _, v := range Map(o, "properties") {
		p, _ := v.(map[string]any)
		if Str(p, "type") == "title" {
			if t := RichText(Arr(p, "title"), false); t != "" {
				return t
			}
		}
	}
	return "(無題)"
}

// PropString renders a page property value as one short line.
func PropString(v Obj) string {
	t := Str(v, "type")
	x, ok := v[t]
	if !ok || x == nil {
		return ""
	}
	switch t {
	case "title", "rich_text":
		return RichText(Arr(v, t), false)
	case "select", "status":
		return Str(Map(v, t), "name")
	case "multi_select":
		return joinNames(Arr(v, t), "name")
	case "people":
		// The integration cannot read users, so names are often missing.
		var out []string
		for _, p := range Arr(v, t) {
			o, _ := p.(map[string]any)
			if n := Str(o, "name"); n != "" {
				out = append(out, n)
			} else if id := Str(o, "id"); len(id) >= 8 {
				out = append(out, id[:8])
			}
		}
		return strings.Join(out, ",")
	case "date":
		d := Map(v, t)
		s := Str(d, "start")
		if e := Str(d, "end"); e != "" {
			s += "→" + e
		}
		return s
	case "created_time", "last_edited_time":
		return JST(Str(v, t))
	case "checkbox":
		if Bool(v, t) {
			return "✓"
		}
		return ""
	case "relation":
		return strconv.Itoa(len(Arr(v, t))) + "件"
	case "formula":
		return scalar(Map(v, t))
	case "rollup":
		r := Map(v, t)
		if Str(r, "type") == "array" {
			var out []string
			for _, i := range Arr(r, "array") {
				o, _ := i.(map[string]any)
				out = append(out, PropString(o))
			}
			return strings.Join(out, ",")
		}
		return scalar(r)
	case "unique_id":
		u := Map(v, t)
		return Str(u, "prefix") + scalar(Obj{"type": "number", "number": u["number"]})
	case "created_by", "last_edited_by":
		return Str(Map(v, t), "name")
	case "files":
		return joinNames(Arr(v, t), "name")
	case "number":
		return scalar(v)
	case "url", "email", "phone_number":
		return Str(v, t)
	}
	return ""
}

// scalar renders the value of a {type: X, X: value} object such as a formula.
func scalar(o Obj) string {
	switch x := o[Str(o, "type")].(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		if x {
			return "✓"
		}
		return ""
	case map[string]any: // date
		return Str(x, "start")
	}
	return ""
}

func joinNames(arr []any, key string) string {
	var out []string
	for _, x := range arr {
		o, _ := x.(map[string]any)
		out = append(out, Str(o, key))
	}
	return strings.Join(out, ",")
}
