package diff

import (
	"reflect"
	"testing"
)

func lines(ns ...int) map[int]bool {
	m := map[int]bool{}
	for _, n := range ns {
		m[n] = true
	}
	return m
}

func TestParseUnified(t *testing.T) {
	t.Parallel()
	in := `diff --git a/a.go b/a.go
index 1..2 100644
--- a/a.go
+++ b/a.go
@@ -3,0 +4,2 @@ func x() {
+	one
+	two
@@ -10 +12 @@
-old
+new
diff --git a/gone.go b/gone.go
--- a/gone.go
+++ /dev/null
@@ -1,2 +0,0 @@
-x
-y
diff --git a/new.go b/new.go
--- /dev/null
+++ b/new.go
@@ -0,0 +1,3 @@
+package x
+
+var y = 1
`
	got := ParseUnified(in)
	want := Changed{
		"a.go":   lines(4, 5, 12),
		"new.go": lines(1, 2, 3),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseUnifiedContext(t *testing.T) {
	t.Parallel()
	// With context lines (default -U3), unchanged lines advance the counter.
	in := `+++ b/a.go
@@ -1,4 +1,5 @@
 keep
+added
 keep
-removed
+changed
 keep
`
	if got := ParseUnified(in)["a.go"]; !reflect.DeepEqual(got, lines(2, 4)) {
		t.Errorf("got %v", got)
	}
}

func TestParseUnifiedIgnoresStray(t *testing.T) {
	t.Parallel()
	if got := ParseUnified("+orphan line\n context\n"); len(got) != 0 {
		t.Errorf("lines before any file header recorded: %v", got)
	}
	if got := ParseUnified(""); len(got) != 0 {
		t.Errorf("empty diff: %v", got)
	}
}

func TestHunkStart(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want int
	}{
		{"@@ -1,2 +3,4 @@", 3},
		{"@@ -1 +7 @@ func", 7},
		{"@@ -1,2 +9 @@", 9},
		{"@@ -1,2 @@", 0},
		{"@@ -1 +x,2 @@", 0},
		{"@@ -1 +12", 12},
	}
	for _, tc := range tests {
		if got := hunkStart(tc.in); got != tc.want {
			t.Errorf("hunkStart(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func FuzzParseUnified(f *testing.F) {
	f.Add("+++ b/a.go\n@@ -1 +1,2 @@\n+a\n+b\n")
	f.Add("@@ -0,0 +1 @@\n+x\n")
	f.Fuzz(func(t *testing.T, in string) {
		for file, set := range ParseUnified(in) {
			if file == "" || file == "/dev/null" {
				t.Fatalf("invalid file key %q", file)
			}
			if len(set) == 0 {
				t.Fatalf("empty line set for %q", file)
			}
		}
	})
}

func TestParseUnifiedAddedLineLooksLikeHeader(t *testing.T) {
	t.Parallel()
	in := "--- a/a.go\n+++ b/a.go\n@@ -1,0 +1,2 @@\n+x\n+++ y\n"
	if got := ParseUnified(in); !reflect.DeepEqual(got, Changed{"a.go": lines(1, 2)}) {
		t.Errorf("got %v", got)
	}
}
