package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSquashGolden(t *testing.T) {
	inputs, err := filepath.Glob("testdata/*.v2.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range inputs {
		for _, links := range []bool{false, true} {
			suffix := ".nn.md"
			if links {
				suffix = ".links.md"
			}
			t.Run(filepath.Base(p)+suffix, func(t *testing.T) {
				b, err := os.ReadFile(p)
				if err != nil {
					t.Fatal(err)
				}
				out := Squash(string(b), links)
				golden := strings.TrimSuffix(p, ".v2.md") + suffix
				want, err := os.ReadFile(golden)
				if err != nil {
					t.Fatal(err)
				}
				if out != string(want) {
					t.Errorf("got:\n%s\nwant:\n%s", out, want)
				}
				if links && WithoutLinks(out) != Squash(string(b), false) {
					t.Error("cached URL projection differs from direct conversion")
				}
			})
		}
	}
}

func TestCodeAndEscapes(t *testing.T) {
	for _, s := range []string{"```html\n<b>x</b>  \n\n\n```\n", "~~~\n<span>x</span>\n~~~\n", "`<b>x</b>`\n", "``a ` <b>x</b>``\n"} {
		if got := Squash(s, false); got != s {
			t.Errorf("code changed: %q -> %q", s, got)
		}
	}
	if got := Squash(`\<b>literal\</b> a\_b`, false); got != "<b>literal</b> a_b\n" {
		t.Errorf("escape: %q", got)
	}
	if got := Squash(`<span color="red"><b>A<i>B</i>C</b></span>`, false); got != "ABC\n" {
		t.Errorf("nested: %q", got)
	}
	if got := Squash(`<new-extension>A</new-extension>`, false); got != "<new-extension>A</new-extension>\n" {
		t.Errorf("future extension: %q", got)
	}
}

// Optional private survey: report only numeric comparisons, never source text,
// filenames, page IDs or titles. Set NN_SURVEY_DIR to a local samples directory.
func TestLocalSurvey(t *testing.T) {
	dir := os.Getenv("NN_SURVEY_DIR")
	if dir == "" {
		t.Skip("NN_SURVEY_DIR not set")
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.v2.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Skip("no local survey")
	}
	for i, p := range files {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal("read survey")
		}
		old, err := os.ReadFile(strings.TrimSuffix(p, ".v2.md") + ".nn.txt")
		if err != nil {
			t.Fatal("read baseline")
		}
		// Saved nn output includes a CLI header; compare body with body.
		_, baseline, ok := strings.Cut(string(old), "\n")
		if !ok {
			t.Fatal("baseline header missing")
		}
		out := Squash(string(raw), false)
		before, after := len([]rune(baseline)), len([]rune(out))
		t.Logf("sample %d: before_body=%d v2=%d after_body=%d ratio=%.3f", i+1, before, len([]rune(string(raw))), after, float64(after)/float64(before))
		if after > before {
			t.Error("converted body exceeds baseline length")
		}
		if WithoutLinks(Squash(string(raw), true)) != out {
			t.Error("cached projection differs (private content withheld)")
		}
	}
}

func TestStyledLinkLabels(t *testing.T) {
	input := "[<b>A</b> `B`](https://example.test)\n"
	if got := Squash(input, false); got != "A `B`\n" {
		t.Fatalf("label: %q", got)
	}
	if got := WithoutLinks(Squash(input, true)); got != Squash(input, false) {
		t.Fatalf("projection: %q", got)
	}
}

func TestEscapedLinkSyntax(t *testing.T) {
	for _, input := range []string{`\[literal\]\(https://example.test\)`, "\\`[code-like](https://example.test)\\`", `a\\b`, `\[text\] [link](https://example.test)`} {
		want := Squash(input, false)
		if got := WithoutLinks(Squash(input, true)); got != want {
			t.Errorf("input=%q got=%q want=%q", input, got, want)
		}
	}
	if got := Squash(`\[literal\]\(https://example.test\)`, false); got != "[literal](https://example.test)\n" {
		t.Errorf("literal link syntax: %q", got)
	}
}
