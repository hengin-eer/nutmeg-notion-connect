package textdiff

import "testing"

func TestUnified(t *testing.T) {
	a := []string{"a", "b", "c", "d", "e", "f"}
	b := []string{"a", "b", "C", "d", "e", "f", "g"}
	got := Unified(a, b, "old", "new", 1)
	want := `--- old
+++ new
@@ -2,3 +2,3 @@
 b
-c
+C
 d
@@ -6,1 +6,2 @@
 f
+g`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestEqual(t *testing.T) {
	if got := Unified([]string{"x"}, []string{"x"}, "a", "b", 1); got != "" {
		t.Errorf("want empty, got %q", got)
	}
}
