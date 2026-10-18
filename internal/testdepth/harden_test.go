package testdepth

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/rossedman/guardrails/internal/runner"
)

// descs renders the mutation descriptions of src's single function.
func descs(t *testing.T, body string) []string {
	t.Helper()
	_, funcs := mutFixture(t, "package pkg\n\n"+body)
	if len(funcs) != 1 {
		t.Fatalf("funcs = %d", len(funcs))
	}
	var out []string
	for _, s := range mutSites(funcs[0]) {
		out = append(out, s.Desc())
	}
	sort.Strings(out)
	return out
}

func TestMutEquivalentBoundaries(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, body string
		want       []string
	}{
		{
			"clamp return operand skips boundary, keeps negation",
			"func Clamp(v, lo int) int {\n\tif v < lo {\n\t\treturn lo\n\t}\n\treturn v\n}\n",
			[]string{"`<` changed to `>=`"},
		},
		{
			"max assignment skips boundary",
			"func Max(xs []int) int {\n\tm := 0\n\tfor _, v := range xs {\n\t\tif v > m {\n\t\t\tm = v\n\t\t}\n\t}\n\treturn m\n}\n",
			[]string{"`>` changed to `<=`"},
		},
		{
			"bool result keeps boundary",
			"func Big(n int) bool {\n\tif n > 3 {\n\t\treturn true\n\t}\n\treturn false\n}\n",
			[]string{"`>` changed to `<=`", "`>` changed to `>=`", "`false` changed to `true`", "`true` changed to `false`"},
		},
		{
			"multi-statement body keeps boundary",
			"func F(v, lo int) int {\n\tif v <= lo {\n\t\tv++\n\t\treturn lo\n\t}\n\treturn v\n}\n",
			[]string{"`++` changed to `--`", "`<=` changed to `<`", "`<=` changed to `>`"},
		},
		{
			"equality condition untouched",
			"func F(v, lo int) int {\n\tif v == lo {\n\t\treturn lo\n\t}\n\treturn v\n}\n",
			[]string{"`==` changed to `!=`"},
		},
		{
			"non-binary condition untouched",
			"func F(ok bool, v int) int {\n\tif ok {\n\t\treturn v\n\t}\n\treturn 0\n}\n",
			nil,
		},
		{
			"compound assignment keeps boundary",
			"func F(v, lo int) int {\n\tif v >= lo {\n\t\tv += lo\n\t}\n\treturn v\n}\n",
			[]string{"`+=` changed to `-=`", "`>=` changed to `<`", "`>=` changed to `>`"},
		},
		{
			"return of a non-operand keeps boundary",
			"func F(v, lo int) int {\n\tif v >= lo {\n\t\treturn 0\n\t}\n\treturn v\n}\n",
			[]string{"`>=` changed to `<`", "`>=` changed to `>`"},
		},
		{
			"two results keep boundary",
			"func F(v, lo int) (int, error) {\n\tif v < lo {\n\t\treturn lo, nil\n\t}\n\treturn v, nil\n}\n",
			[]string{"`<` changed to `<=`", "`<` changed to `>=`"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := descs(t, tc.body); !slices.Equal(got, tc.want) {
				t.Errorf("mutations = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMutIsBoundary(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		from, to token.Token
		want     bool
	}{
		{token.LSS, token.LEQ, true},
		{token.LEQ, token.LSS, true},
		{token.GTR, token.GEQ, true},
		{token.GEQ, token.GTR, true},
		{token.LSS, token.GEQ, false},
		{token.GTR, token.LEQ, false},
		{token.EQL, token.NEQ, false},
	} {
		if got := mutIsBoundary(tc.from, tc.to); got != tc.want {
			t.Errorf("mutIsBoundary(%s, %s) = %v", tc.from, tc.to, got)
		}
	}
}

func TestMutSourcePrinterError(t *testing.T) {
	t.Parallel()
	// A node the printer can't print yields "", which never equals an
	// operand, so nothing is treated as equivalent by accident.
	if got := mutSource(token.NewFileSet(), &ast.BadExpr{}); got != "BadExpr" && got != "" {
		t.Logf("mutSource(BadExpr) = %q", got)
	}
	fset := token.NewFileSet()
	e, err := parser.ParseExprFrom(fset, "", "a.b", 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := mutSource(fset, e); got != "a.b" {
		t.Errorf("mutSource = %q", got)
	}
}

const suggestSource = `package codec

import "sort"

// guardrails:hot
func Hot(x int) int { return x }

func SortNames(xs []string) { sort.Strings(xs) }

func EncodeCovered(x int) int { return x }

// guardrails:nobench not worth it
func EncodeSkip(x int) int { return x }

func Add(a, b int) int { return a + b }
`

const suggestTests = `package codec

import "testing"

func BenchmarkCovered(b *testing.B) {
	for b.Loop() {
		EncodeCovered(1)
	}
}
`

func TestBenchSuggestions(t *testing.T) {
	f := newFuzzFixture(t)
	src := f.write("codec.go", suggestSource)
	f.write("codec_test.go", suggestTests)
	funcs := f.funcs(src)

	got := BenchSuggestions(f.env(), funcs, nil)
	want := []string{
		"internal/codec/codec.go:6  Hot  could use a Benchmark (marked // guardrails:hot)",
		"internal/codec/codec.go:8  SortNames  could use a Benchmark (sorts)",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("suggestions:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// A performance-critical package makes every changed function a
	// candidate, except those covered or opted out.
	got = BenchSuggestions(f.env(), funcs, []string{"internal/..."})
	if len(got) != 3 || !strings.Contains(got[2], "Add  could use a Benchmark (package is listed in go.bench.packages)") {
		t.Errorf("perf package suggestions = %q", got)
	}
	if len(f.fake.Calls()) != 0 {
		t.Errorf("suggestions ran commands: %v", f.fake.Calls())
	}
}

func TestBenchSuggestionsUnparseableTests(t *testing.T) {
	f := newFuzzFixture(t)
	src := f.write("codec.go", suggestSource)
	f.write("bad_test.go", "package codec\n\nfunc BenchmarkBroken(\n")
	// Without an index nothing counts as covered.
	if got := BenchSuggestions(f.env(), f.funcs(src), nil); len(got) != 3 {
		t.Errorf("suggestions = %q", got)
	}
}

func TestChanges(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	newFile := filepath.Join(repo, "pkg", "new.go")
	if err := os.MkdirAll(filepath.Dir(newFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newFile, []byte("package pkg\n\nfunc New() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := runner.NewFake("git")
	f.On("git diff", runner.Result{Stdout: "diff --git a/pkg/a.go b/pkg/a.go\n--- a/pkg/a.go\n+++ b/pkg/a.go\n@@ -3,0 +4,2 @@\n+x\n+y\n"})
	f.On("git ls-files", runner.Result{Stdout: "pkg/new.go\npkg/gone.go\n"})

	changed, touched, err := Changes(context.Background(), f, repo, "main")
	if err != nil {
		t.Fatal(err)
	}
	a := filepath.Join(repo, "pkg", "a.go")
	if got := changed[a]; !got[4] || !got[5] || len(got) != 2 {
		t.Errorf("a.go lines = %v", got)
	}
	if got := changed[newFile]; len(got) != 4 || !got[1] || !got[4] {
		t.Errorf("untracked file should be fully changed: %v", got)
	}
	if got := changed[filepath.Join(repo, "pkg", "gone.go")]; len(got) != 0 {
		t.Errorf("unreadable untracked file = %v", got)
	}
	sort.Strings(touched)
	if len(touched) != 3 || touched[0] != a {
		t.Errorf("touched = %v", touched)
	}
	if !f.Ran("git diff --unified=0 --no-color --no-ext-diff --src-prefix=a/ --dst-prefix=b/ main -- *.go") {
		t.Errorf("calls = %v", f.Calls())
	}
	for _, c := range f.Calls() {
		if c.Dir != repo {
			t.Errorf("ran in %s", c.Dir)
		}
	}
}

func TestChangesErrors(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()

	f := runner.NewFake("git").On("git diff", runner.Result{ExitCode: 128, Stderr: "fatal: bad revision 'nope'\n"})
	if _, _, err := Changes(context.Background(), f, repo, "nope"); err == nil || !strings.Contains(err.Error(), "bad revision") {
		t.Errorf("non-zero diff: %v", err)
	}

	f = runner.NewFake("git").OnError("git diff", errors.New("no git"))
	if _, _, err := Changes(context.Background(), f, repo, "HEAD"); err == nil || !strings.Contains(err.Error(), "no git") {
		t.Errorf("run error: %v", err)
	}

	// ls-files failing is tolerated: the diff alone is used.
	f = runner.NewFake("git").On("git ls-files", runner.Result{ExitCode: 1})
	changed, touched, err := Changes(context.Background(), f, repo, "HEAD")
	if err != nil || len(changed) != 0 || len(touched) != 0 {
		t.Errorf("ls-files failure: %v %v %v", changed, touched, err)
	}
	f = runner.NewFake("git").OnError("git ls-files", errors.New("boom"))
	if _, _, err := Changes(context.Background(), f, repo, "HEAD"); err != nil {
		t.Errorf("ls-files error should be tolerated: %v", err)
	}
}
