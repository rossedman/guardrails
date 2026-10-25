package glob

import (
	"fmt"
	"strings"
	"testing"
)

func BenchmarkMatch(b *testing.B) {
	cases := []struct{ pattern, name string }{
		{"**/*.pb.go", "api/v1/internal/service/svc.pb.go"},
		{"cmd/**/main.go", "cmd/app/server/main.go"},
		{"**/mocks/**", "internal/store/postgres/queries.go"},
		{"kind-*", "kind-local"},
	}
	b.ReportAllocs()
	for b.Loop() {
		for _, c := range cases {
			Match(c.pattern, c.name)
		}
	}
}

func BenchmarkAny(b *testing.B) {
	patterns := []string{"**/*.pb.go", "**/mocks/**", "**/testdata/**", "vendor/**", "cmd/**/main.go", "**/*_gen.go", "docs/**/*.md", "*.lock"}
	// Every file against every pattern, as the gate does.
	files := make([]string, 200)
	for i := range files {
		files[i] = fmt.Sprintf("internal/pkg%d/sub/file%d.go", i%20, i)
	}
	b.ReportAllocs()
	for b.Loop() {
		for _, f := range files {
			Any(patterns, f)
		}
	}
}

func FuzzMatch(f *testing.F) {
	seeds := [][2]string{
		{"**/mocks/**", "internal/mocks/m.go"},
		{"**/mocks/**", "mocks"},
		{"**/*.pb.go", "api/v1/svc.pb.go"},
		{"cmd/**/main.go", "cmd/a/b/main.go"},
		{"kind-*", "kind-local"},
		{"*-dev", "eu-prod"},
		{"a/b", "a/b/c"},
		{"/a/b/", "a/b"},
		{"", ""},
		{"**", ""},
		{"**", "any/thing"},
		{"[", "x"},
		{"a/[b-", "a/b"},
		{"a\\", "a\\"},
		{"**/**/**/x", "a/b/c/d/x"},
		{"a//b", "a//b"},
	}
	for _, s := range seeds {
		f.Add(s[0], s[1])
	}
	f.Fuzz(func(t *testing.T, pattern, name string) {
		got := Match(pattern, name)
		if got != Match(pattern, name) {
			t.Fatalf("Match(%q, %q) not deterministic", pattern, name)
		}
		if !Match("**", name) {
			t.Fatalf("Match(\"**\", %q) = false", name)
		}
		if Any([]string{pattern}, name) != got {
			t.Fatalf("Any([%q], %q) != Match", pattern, name)
		}
		// Leading and trailing slashes are ignored on both sides.
		if Match("/"+pattern+"/", "/"+name+"/") != got {
			t.Fatalf("slashes change Match(%q, %q)", pattern, name)
		}
		// A pattern with no metacharacters is a literal and matches itself.
		if !strings.ContainsAny(pattern, `*?[\`) && !Match(pattern, pattern) {
			t.Fatalf("literal %q does not match itself", pattern)
		}
		// "**/" prefix only adds the zero-directory case.
		if got && !strings.HasPrefix(pattern, "/") && !Match("**/"+pattern, name) {
			t.Fatalf("Match(%q, %q) but not with **/ prefix", pattern, name)
		}
	})
}

func FuzzAny(f *testing.F) {
	f.Add("**/*.pb.go", "cmd/**/main.go", "[", "cmd/x/main.go")
	f.Add("", "**", "a", "")
	f.Add("kind-*", "*-dev", "x", "eu-dev")
	f.Fuzz(func(t *testing.T, p1, p2, p3, name string) {
		ps := []string{p1, p2, p3}
		want := false
		for _, p := range ps {
			want = want || Match(p, name)
		}
		if got := Any(ps, name); got != want {
			t.Fatalf("Any(%q, %q) = %v, want %v", ps, name, got, want)
		}
		if Any(nil, name) {
			t.Fatal("Any(nil) = true")
		}
	})
}
