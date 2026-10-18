package coverage

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const sample = `mode: atomic
example.com/m/a/a.go:3.20,5.2 2 1
example.com/m/a/a.go:7.20,9.2 2 0
example.com/m/b/b.go:3.20,5.2 4 0
example.com/m/a/a.go:3.20,5.2 2 0
`

func mustParse(t testing.TB, s string) *Profile {
	t.Helper()
	p, err := Parse(strings.NewReader(s))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParse(t *testing.T) {
	t.Parallel()
	p := mustParse(t, sample)
	if p.Mode != "atomic" {
		t.Errorf("Mode = %q", p.Mode)
	}
	// The duplicate block is merged, keeping the highest count.
	if len(p.Blocks) != 3 {
		t.Fatalf("got %d blocks, want 3", len(p.Blocks))
	}
	want := Block{File: "example.com/m/a/a.go", StartLine: 3, StartCol: 20, EndLine: 5, EndCol: 2, NumStmt: 2, Count: 1}
	if p.Blocks[0] != want {
		t.Errorf("block 0 = %+v, want %+v", p.Blocks[0], want)
	}
}

func TestParseMergeKeepsMax(t *testing.T) {
	t.Parallel()
	p := mustParse(t, "mode: set\nx/a.go:1.1,2.2 1 0\n\nx/a.go:1.1,2.2 1 5\n")
	if len(p.Blocks) != 1 || p.Blocks[0].Count != 5 {
		t.Errorf("blocks = %+v", p.Blocks)
	}
}

func TestParseErrors(t *testing.T) {
	t.Parallel()
	tests := []string{
		"no-colon-here 1 1",
		"x/a.go:1.1,2.2 1",
		"x/a.go:1.1-2.2 1 1",
		"x/a.go:1.1,2.2 x 1",
		"x/a.go:1.1,2.2 1 y",
	}
	for _, in := range tests {
		if _, err := Parse(strings.NewReader("mode: set\n" + in + "\n")); err == nil || !strings.Contains(err.Error(), "line 2") {
			t.Errorf("Parse(%q) err = %v, want line 2 error", in, err)
		}
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func TestParseReadError(t *testing.T) {
	t.Parallel()
	if _, err := Parse(errReader{}); err == nil {
		t.Error("expected read error")
	}
}

func TestStats(t *testing.T) {
	t.Parallel()
	p := mustParse(t, sample)
	if got := p.Total(); got != (Stats{Covered: 2, Total: 8}) {
		t.Errorf("Total = %+v", got)
	}
	if got := p.Total().Percent(); got != 25 {
		t.Errorf("Percent = %v", got)
	}
	if (Stats{}).Percent() != 100 {
		t.Error("empty stats should be 100%")
	}
	byPkg := p.ByPackage()
	want := map[string]Stats{
		"example.com/m/a": {Covered: 2, Total: 4},
		"example.com/m/b": {Covered: 0, Total: 4},
	}
	if !reflect.DeepEqual(byPkg, want) {
		t.Errorf("ByPackage = %+v", byPkg)
	}
	if got := p.Packages(); !reflect.DeepEqual(got, []string{"example.com/m/a", "example.com/m/b"}) {
		t.Errorf("Packages = %v", got)
	}
}

func TestFilter(t *testing.T) {
	t.Parallel()
	p := mustParse(t, sample)
	f := p.Filter(func(file string) bool { return !strings.Contains(file, "/b/") })
	if f.Mode != p.Mode || len(f.Blocks) != 2 {
		t.Errorf("Filter = %+v", f)
	}
	if len(p.Blocks) != 3 {
		t.Error("Filter mutated the original")
	}
}

const src = `package a

type Queue[T any] struct{ items []T }

type Pair[K comparable, V any] struct{}

func Plain() int {
	return 1
}

func (q *Queue[T]) Drain() []T {
	out := q.items
	q.items = nil
	return out
}

func (p Pair[K, V]) Get() {}

func (Pair[K, V]) Unnamed() {}

func (q Queue[T]) Len() int {
	return len(q.items)
}

func decl()
`

func writeSrc(t testing.TB) (dir string, resolve Resolver) {
	t.Helper()
	dir = t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken.go"), []byte("package"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, func(f string) (string, bool) {
		rel, ok := strings.CutPrefix(f, "m/")
		if !ok {
			return "", false
		}
		return filepath.Join(dir, rel), true
	}
}

func TestFuncs(t *testing.T) {
	t.Parallel()
	dir, resolve := writeSrc(t)
	p := mustParse(t, `mode: set
m/a.go:7.18,9.2 1 1
m/a.go:11.32,15.2 3 0
m/a.go:21.29,23.2 1 0
m/broken.go:1.1,1.2 1 0
other/x.go:1.1,2.2 1 0
`)
	funcs := p.Funcs(resolve)
	file := filepath.Join(dir, "a.go")
	want := []Func{
		{File: file, Line: 7, Name: "Plain", Stats: Stats{Covered: 1, Total: 1}},
		{File: file, Line: 11, Name: "(*Queue).Drain", Stats: Stats{Covered: 0, Total: 3}},
		{File: file, Line: 21, Name: "(Queue).Len", Stats: Stats{Covered: 0, Total: 1}},
	}
	if !reflect.DeepEqual(funcs, want) {
		t.Errorf("Funcs =\n%+v\nwant\n%+v", funcs, want)
	}

	worst := WorstFuncs(funcs, 1)
	if len(worst) != 1 || worst[0].Name != "(*Queue).Drain" || worst[0].Uncovered() != 3 {
		t.Errorf("WorstFuncs = %+v", worst)
	}
	if got := WorstFuncs(funcs, 10); len(got) != 2 {
		t.Errorf("fully covered funcs should be dropped: %+v", got)
	}
}

func TestFuncsSortsAcrossFiles(t *testing.T) {
	t.Parallel()
	dir, resolve := writeSrc(t)
	if err := os.WriteFile(filepath.Join(dir, "0.go"), []byte("package a\n\nfunc Z() {\n\t_ = 1\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := mustParse(t, "mode: set\nm/a.go:7.18,9.2 1 1\nm/0.go:3.10,5.2 1 1\n")
	funcs := p.Funcs(resolve)
	if len(funcs) != 2 || funcs[0].Name != "Z" {
		t.Errorf("Funcs not sorted by file: %+v", funcs)
	}
}

func TestFuncNameReceivers(t *testing.T) {
	t.Parallel()
	dir, _ := writeSrc(t)
	spans, err := funcSpans(filepath.Join(dir, "a.go"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range spans {
		names = append(names, s.name)
	}
	want := []string{"Plain", "(*Queue).Drain", "(Pair).Get", "(Pair).Unnamed", "(Queue).Len"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("names = %v, want %v", names, want)
	}
}

func TestPatch(t *testing.T) {
	t.Parallel()
	resolve := func(f string) (string, bool) {
		if f == "skip.go" {
			return "", false
		}
		return "/r/" + f, true
	}
	p := mustParse(t, `mode: set
a.go:1.1,3.2 2 1
a.go:3.5,6.2 2 0
a.go:10.1,10.9 0 0
b.go:1.1,2.2 1 0
skip.go:1.1,2.2 1 0
`)
	changed := map[string]LineSet{
		"/r/a.go": {2: true, 3: true, 5: true, 8: true, 10: true},
		"/r/c.go": {1: true},
	}
	got := p.Patch(changed, resolve)
	// Line 2 covered; line 3 is in both blocks and counts as covered;
	// line 5 uncovered; line 8 is outside every block; line 10 has no
	// statements; b.go has no changes.
	if got.Stats != (Stats{Covered: 2, Total: 3}) {
		t.Errorf("Stats = %+v", got.Stats)
	}
	if !reflect.DeepEqual(got.Uncovered, map[string][]int{"/r/a.go": {5}}) {
		t.Errorf("Uncovered = %v", got.Uncovered)
	}
}

func TestPatchSortsUncovered(t *testing.T) {
	t.Parallel()
	p := mustParse(t, "mode: set\na.go:1.1,9.2 3 0\n")
	got := p.Patch(map[string]LineSet{"a.go": {9: true, 2: true, 5: true}}, func(f string) (string, bool) { return f, true })
	if !reflect.DeepEqual(got.Uncovered["a.go"], []int{2, 5, 9}) {
		t.Errorf("Uncovered = %v", got.Uncovered)
	}
}

func FuzzParse(f *testing.F) {
	f.Add(sample)
	f.Add("mode: set\nx.go:1.2,3.4 5 6\n")
	f.Add("x.go:1.2,3.4 5")
	f.Fuzz(func(t *testing.T, in string) {
		p, err := Parse(strings.NewReader(in))
		if err != nil {
			return
		}
		// Invariants: totals are consistent and percent is bounded.
		s := p.Total()
		var sum Stats
		for _, ps := range p.ByPackage() {
			sum.Covered += ps.Covered
			sum.Total += ps.Total
		}
		if sum != s {
			t.Fatalf("package sums %+v != total %+v", sum, s)
		}
		nonNegative := true
		for _, b := range p.Blocks {
			nonNegative = nonNegative && b.NumStmt >= 0
		}
		if pct := s.Percent(); nonNegative && (pct < 0 || pct > 100) {
			t.Fatalf("percent %v out of range for %+v", pct, s)
		}
	})
}

func BenchmarkParse(b *testing.B) {
	var sb strings.Builder
	sb.WriteString("mode: atomic\n")
	for i := 0; i < 5000; i++ {
		sb.WriteString("example.com/m/pkg/file.go:")
		sb.WriteString(strings.Repeat("1", 1+i%3))
		sb.WriteString(".2,40.3 3 1\n")
	}
	in := sb.String()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Parse(strings.NewReader(in)); err != nil {
			b.Fatal(err)
		}
	}
}
