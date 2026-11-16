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
		out := Squash(string(raw), false)
		t.Logf("sample %d: before=%d v2=%d after=%d ratio=%.3f", i+1, len([]rune(string(old))), len([]rune(string(raw))), len([]rune(out)), float64(len([]rune(out)))/float64(len([]rune(string(old)))))
	}
}
