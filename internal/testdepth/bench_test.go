package testdepth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rossedman/guardrails/internal/config"
	"github.com/rossedman/guardrails/internal/coverage"
	"github.com/rossedman/guardrails/internal/module"
	"github.com/rossedman/guardrails/internal/runner"
)

// benchFixture is a module at repo/mod with packages written by the test.
type benchFixture struct {
	repo, root string
	fake       *runner.Fake
	cfg        config.Config
}

func newBenchFixture(t *testing.T) *benchFixture {
	t.Helper()
	repo := t.TempDir()
	root := filepath.Join(repo, "mod")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	return &benchFixture{repo: repo, root: root, fake: runner.NewFake("go", "git"), cfg: config.Default()}
}

func (f *benchFixture) write(t *testing.T, rel, src string) string {
	t.Helper()
	p := filepath.Join(f.root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func (f *benchFixture) env(base string, asked map[string]bool) Env {
	return Env{
		Env:  module.Env{Runner: f.fake, Config: f.cfg, Repo: f.repo},
		Root: f.root, ModPath: "example.com/mod", Base: base, Asked: asked, Now: time.Now,
	}
}

// funcs returns the functions in the files, treating every line as changed.
func funcsOf(t *testing.T, files ...string) []Func {
	t.Helper()
	changed := map[string]coverage.LineSet{}
	for _, f := range files {
		set := coverage.LineSet{}
		for i := 1; i <= 200; i++ {
			set[i] = true
		}
		changed[f] = set
	}
	fs, err := ChangedFuncs(changed)
	if err != nil {
		t.Fatal(err)
	}
	return fs
}

const benchCodec = `package codec

import "encoding/binary"

// guardrails:hot
func Hot(b []byte) int { return len(b) }

func DecodeLen(b []byte) uint32 { return binary.BigEndian.Uint32(b) }

func Covered(b []byte) uint32 { return binary.BigEndian.Uint32(b) }

// guardrails:nobench constant time
func Skipped(b []byte) uint32 { return binary.BigEndian.Uint32(b) }

func plain() int { return 1 }
`

const benchCodecTest = `package codec

import "testing"

func BenchmarkCovered(b *testing.B) {
	for i := 0; i < b.N; i++ {
		Covered([]byte{1, 2, 3, 4})
	}
}
`

func TestBenchRequirements(t *testing.T) {
	t.Parallel()
	f := newBenchFixture(t)
	src := f.write(t, "codec/codec.go", benchCodec)
	f.write(t, "codec/codec_test.go", benchCodecTest)
	funcs := funcsOf(t, src)

	res := Benchmarks(context.Background(), f.env("", nil), funcs)
	if len(res.Checks) != 1 {
		t.Fatalf("checks = %+v", res.Checks)
	}
	c := res.Checks[0]
	if c.Name != "benchmarks" || !c.Failed || c.Summary != "2 hot-path functions have no benchmark" {
		t.Errorf("check = %+v", c)
	}
	joined := strings.Join(c.Lines, "\n")
	for _, want := range []string{
		"mod/codec/codec.go:6  Hot  needs a Benchmark (marked // guardrails:hot",
		"mod/codec/codec.go:8  DecodeLen  needs a Benchmark (DecodeLen does serialization",
		"uses encoding/binary) — or say in one line why not",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("lines missing %q:\n%s", want, joined)
		}
	}
	for _, absent := range []string{"Covered", "Skipped", "plain"} {
		if strings.Contains(joined, absent) {
			t.Errorf("lines should not mention %s:\n%s", absent, joined)
		}
	}
	if strings.Contains(c.Lines[0], "why not") {
		t.Errorf("strong signal should block without asking: %q", c.Lines[0])
	}
	if len(res.Asked) != 1 || res.Asked[0] != "bench:codec.DecodeLen" {
		t.Errorf("asked = %v", res.Asked)
	}
	if !strings.Contains(c.Fix, "guardrails:nobench") || len(res.Ran) != 1 || res.Ran[0] != "benchmarks" {
		t.Errorf("fix/ran = %q %v", c.Fix, res.Ran)
	}

	// Asked once: the weak requirement isn't raised again; the strong one is.
	res = Benchmarks(context.Background(), f.env("", map[string]bool{"bench:codec.DecodeLen": true}), funcs)
	if len(res.Checks) != 1 || res.Checks[0].Summary != "1 hot-path function has no benchmark" || len(res.Asked) != 0 {
		t.Errorf("after asking: %+v asked=%v", res.Checks, res.Asked)
	}

	// Disabled requirements: nothing.
	f.cfg.Go.Bench.RequireOnHotPaths = false
	if res := Benchmarks(context.Background(), f.env("", nil), funcs); len(res.Checks) != 0 || len(res.Ran) != 0 {
		t.Errorf("disabled: %+v", res)
	}
}

func TestBenchRequirementsPackagesAndIndexErrors(t *testing.T) {
	t.Parallel()
	f := newBenchFixture(t)
	f.cfg.Go.Bench.Packages = []string{"./internal/..."}
	src := f.write(t, "internal/x/x.go", "package x\n\nfunc Add(a, b int) int { return a + b }\n")
	funcs := funcsOf(t, src)
	res := Benchmarks(context.Background(), f.env("", nil), funcs)
	if len(res.Checks) != 1 || !strings.Contains(res.Checks[0].Lines[0], "go.bench.packages") || len(res.Asked) != 0 {
		t.Fatalf("perf package: %+v", res)
	}

	// An unparsable test file is a note, not a failure.
	f.write(t, "internal/x/x_test.go", "package x\nfunc {")
	res = Benchmarks(context.Background(), f.env("", nil), funcs)
	if len(res.Checks) != 0 || len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "benchmark index skipped") {
		t.Errorf("index error: %+v", res)
	}
}

// benchLines renders n runs of one benchmark at the given ns/op values.
func benchLines(name string, ns []float64, allocs float64) string {
	var b strings.Builder
	for _, v := range ns {
		fmt.Fprintf(&b, "%s-8 \t 1000000\t %g ns/op\t 64 B/op\t %g allocs/op\n", name, v, allocs)
	}
	return b.String() + "PASS\n"
}

// regressionFixture sets up a covered hot function and a fake go/git.
func regressionFixture(t *testing.T) (*benchFixture, []Func) {
	t.Helper()
	f := newBenchFixture(t)
	f.cfg.Go.Bench.RequireOnHotPaths = false
	src := f.write(t, "codec/codec.go", benchCodec)
	f.write(t, "codec/codec_test.go", benchCodecTest)
	f.fake.OnFunc("git worktree add", func(c runner.Call) (runner.Result, error) {
		// args: worktree add --detach <tmp> <base>
		tmp := c.Args[3]
		return runner.Result{}, os.MkdirAll(filepath.Join(tmp, "mod", "codec"), 0o755)
	})
	return f, funcsOf(t, src)
}

func TestBenchRegression(t *testing.T) {
	t.Parallel()
	f, funcs := regressionFixture(t)
	// No ties in time/op, so the exact test applies: p = 2/924.
	baseOut := benchLines("BenchmarkCovered", []float64{97, 98, 99, 101, 102, 103}, 0)
	headOut := benchLines("BenchmarkCovered", []float64{127, 128, 129, 131, 132, 133}, 1)
	f.fake.OnFunc("go test", func(c runner.Call) (runner.Result, error) {
		if c.Dir == f.root {
			return runner.Result{Stdout: headOut}, nil
		}
		return runner.Result{Stdout: baseOut}, nil
	})
	res := Benchmarks(context.Background(), f.env("abc123", nil), funcs)
	if len(res.Checks) != 1 {
		t.Fatalf("checks = %+v notes=%v", res.Checks, res.Notes)
	}
	c := res.Checks[0]
	if c.Name != "bench-regression" || c.Summary != "1 benchmark regressed vs the session's base commit" {
		t.Errorf("check = %+v", c)
	}
	joined := strings.Join(c.Lines, "\n")
	for _, want := range []string{
		"BenchmarkCovered  time/op  +30.0% (p=0.002)  100ns → 130ns",
		"BenchmarkCovered  allocs/op  +∞% (p=0.001)  0 → 1",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("lines missing %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "B/op ") {
		t.Errorf("unchanged B/op reported:\n%s", joined)
	}
	if !strings.Contains(strings.Join(res.Ran, ","), "bench-regression") {
		t.Errorf("ran = %v", res.Ran)
	}

	// The command and cleanup.
	var sawBench, sawRemove, sawPrune bool
	for _, call := range f.fake.Calls() {
		line := call.String()
		switch {
		case strings.HasPrefix(line, "go test -run ^$ -bench ^(BenchmarkCovered)$ -benchmem -count=6 -benchtime=100ms ./codec"):
			sawBench = true
		case strings.HasPrefix(line, "git worktree remove --force"):
			sawRemove = call.Dir == f.repo
		case line == "git worktree prune":
			sawPrune = true
		}
	}
	if !sawBench || !sawRemove || !sawPrune {
		t.Errorf("calls: bench=%v remove=%v prune=%v\n%v", sawBench, sawRemove, sawPrune, f.fake.Calls())
	}
}

func TestBenchRegressionNotSignificantOrBelowThreshold(t *testing.T) {
	t.Parallel()
	tests := map[string][2][]float64{
		"noise":           {{100, 120, 90, 110, 95, 105}, {104, 98, 115, 92, 108, 101}},
		"below threshold": {{100, 101, 99, 100, 102, 98}, {105, 106, 104, 105, 107, 103}},
		"faster":          {{100, 101, 99, 100, 102, 98}, {50, 51, 49, 50, 52, 48}},
	}
	for name, s := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f, funcs := regressionFixture(t)
			f.fake.OnFunc("go test", func(c runner.Call) (runner.Result, error) {
				if c.Dir == f.root {
					return runner.Result{Stdout: benchLines("BenchmarkCovered", s[1], 0)}, nil
				}
				return runner.Result{Stdout: benchLines("BenchmarkCovered", s[0], 0)}, nil
			})
			if res := Benchmarks(context.Background(), f.env("abc", nil), funcs); len(res.Checks) != 0 || len(res.Notes) != 0 {
				t.Errorf("%s: %+v", name, res)
			}
		})
	}
}

func TestBenchRegressionSkips(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("no base", func(t *testing.T) {
		t.Parallel()
		f, funcs := regressionFixture(t)
		if res := Benchmarks(ctx, f.env("", nil), funcs); len(res.Ran) != 0 || len(f.fake.Calls()) != 0 {
			t.Errorf("%+v %v", res, f.fake.Calls())
		}
	})
	t.Run("no covering benchmarks", func(t *testing.T) {
		t.Parallel()
		f := newBenchFixture(t)
		src := f.write(t, "x/x.go", "package x\n\nfunc plain() int { return 1 }\n")
		if res := Benchmarks(ctx, f.env("abc", nil), funcsOf(t, src)); len(res.Ran) != 1 || len(f.fake.Calls()) != 0 {
			t.Errorf("%+v", res)
		}
	})
	t.Run("head run fails", func(t *testing.T) {
		t.Parallel()
		f, funcs := regressionFixture(t)
		f.fake.On("go test", runner.Result{ExitCode: 1, Stderr: "\n  build failed\n"})
		res := Benchmarks(ctx, f.env("abc", nil), funcs)
		if len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "exited 1: build failed") {
			t.Errorf("%+v", res)
		}
	})
	t.Run("head run error", func(t *testing.T) {
		t.Parallel()
		f, funcs := regressionFixture(t)
		f.fake.OnError("go test", errors.New("boom"))
		if res := Benchmarks(ctx, f.env("abc", nil), funcs); len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "boom") {
			t.Errorf("%+v", res)
		}
	})
	t.Run("worktree add fails", func(t *testing.T) {
		t.Parallel()
		f, funcs := regressionFixture(t)
		f.fake.On("go test", runner.Result{Stdout: benchLines("BenchmarkCovered", []float64{1, 2}, 0)})
		f.fake.OnFunc("git worktree add", func(runner.Call) (runner.Result, error) {
			return runner.Result{ExitCode: 128, Stderr: "fatal: invalid reference: abc"}, nil
		})
		res := Benchmarks(ctx, f.env("abc", nil), funcs)
		if len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "invalid reference") {
			t.Errorf("%+v", res)
		}
	})
	t.Run("worktree add error", func(t *testing.T) {
		t.Parallel()
		f, funcs := regressionFixture(t)
		f.fake.On("go test", runner.Result{Stdout: benchLines("BenchmarkCovered", []float64{1, 2}, 0)})
		f.fake.OnFunc("git worktree add", func(runner.Call) (runner.Result, error) {
			return runner.Result{}, errors.New("no git")
		})
		if res := Benchmarks(ctx, f.env("abc", nil), funcs); len(res.Notes) != 1 {
			t.Errorf("%+v", res)
		}
	})
	t.Run("package or benchmark missing at base", func(t *testing.T) {
		t.Parallel()
		f, funcs := regressionFixture(t)
		f.fake.OnFunc("git worktree add", func(runner.Call) (runner.Result, error) { return runner.Result{}, nil })
		f.fake.On("go test", runner.Result{Stdout: benchLines("BenchmarkCovered", []float64{1, 1, 1}, 0)})
		if res := Benchmarks(ctx, f.env("abc", nil), funcs); len(res.Checks) != 0 || len(res.Notes) != 0 {
			t.Errorf("%+v", res)
		}
	})
	t.Run("base run fails", func(t *testing.T) {
		t.Parallel()
		f, funcs := regressionFixture(t)
		f.fake.OnFunc("go test", func(c runner.Call) (runner.Result, error) {
			if c.Dir == f.root {
				return runner.Result{Stdout: benchLines("BenchmarkCovered", []float64{1, 1, 1}, 0)}, nil
			}
			return runner.Result{ExitCode: 2, Stderr: "no such benchmark"}, nil
		})
		if res := Benchmarks(ctx, f.env("abc", nil), funcs); len(res.Checks) != 0 || len(res.Notes) != 0 {
			t.Errorf("%+v", res)
		}
	})
	t.Run("budget exceeded", func(t *testing.T) {
		t.Parallel()
		f, funcs := regressionFixture(t)
		f.cfg.Go.Bench.BudgetSeconds = 40
		f.fake.OnFunc("go test", func(c runner.Call) (runner.Result, error) {
			if c.Dir == f.root {
				return runner.Result{Stdout: benchLines("BenchmarkCovered", []float64{1, 1, 1}, 0)}, nil
			}
			return runner.Result{}, context.DeadlineExceeded
		})
		ctx, cancel := context.WithTimeout(ctx, time.Nanosecond)
		defer cancel()
		time.Sleep(time.Millisecond)
		res := Benchmarks(ctx, f.env("abc", nil), funcs)
		if len(res.Notes) != 1 || res.Notes[0] != "benchmark comparison skipped: exceeded the 40s budget" {
			t.Errorf("%+v", res)
		}
	})
}

func TestBenchCompareSkipsMissingMetrics(t *testing.T) {
	t.Parallel()
	f := newBenchFixture(t)
	base := benchSamples{"BenchmarkA": {"ns/op": {1, 1, 1}}}
	head := benchSamples{
		"BenchmarkA":   {"ns/op": {1, 1, 1}, "B/op": {1, 2, 3}},
		"BenchmarkNew": {"ns/op": {5, 5, 5}},
	}
	if _, ok := benchCompare(f.env("abc", nil), base, head); ok {
		t.Error("nothing comparable should regress")
	}
	if benchDisplay(f.env("", nil), "/elsewhere/x.go") != "/elsewhere/x.go" || benchFirstLine("  \n") != "" {
		t.Error("helpers")
	}
	multi := benchSamples{"BenchmarkA": {"ns/op": {1, 1, 1, 1, 1, 1}}, "BenchmarkB": {"ns/op": {1, 1, 1, 1, 1, 1}}}
	slow := benchSamples{"BenchmarkA": {"ns/op": {9, 9, 9, 9, 9, 9}}, "BenchmarkB": {"ns/op": {9, 9, 9, 9, 9, 9}}}
	if c, ok := benchCompare(f.env("abc", nil), multi, slow); !ok || c.Summary != "2 benchmarks regressed vs the session's base commit" {
		t.Errorf("%+v", c)
	}
}

// TestBenchRegressionIntegration runs real benchmarks at a base commit and
// a deliberately slower head.
func TestBenchRegressionIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("runs real benchmarks")
	}
	for _, tool := range []string{"go", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed", tool)
		}
	}
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(rel, src string) string {
		t.Helper()
		p := filepath.Join(repo, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("go.mod", "module example.com/b\n\ngo 1.22\n")
	src := write("sum/sum.go", "package sum\n\n// guardrails:hot\nfunc Sum(xs []int) int {\n\tt := 0\n\tfor _, x := range xs {\n\t\tt += x\n\t}\n\treturn t\n}\n")
	write("sum/sum_test.go", "package sum\n\nimport \"testing\"\n\nvar sink int\n\nfunc BenchmarkSum(b *testing.B) {\n\txs := make([]int, 1000)\n\tfor i := 0; i < b.N; i++ {\n\t\tsink = Sum(xs)\n\t}\n}\n")
	git("init", "-q")
	git("add", "-A")
	git("commit", "-qm", "base")
	base := git("rev-parse", "HEAD")
	write("sum/sum.go", "package sum\n\n// guardrails:hot\nfunc Sum(xs []int) int {\n\tt := 0\n\tfor r := 0; r < 20; r++ {\n\t\tfor _, x := range xs {\n\t\t\tt += x * r\n\t\t}\n\t}\n\treturn t\n}\n")

	cfg := config.Default()
	cfg.Go.Bench.Benchtime = "20ms"
	cfg.Go.Bench.BudgetSeconds = 120
	e := Env{
		Env:  module.Env{Runner: runner.Exec{}, Config: cfg, Repo: repo},
		Root: repo, ModPath: "example.com/b", Base: base, Now: time.Now,
	}
	res := Benchmarks(context.Background(), e, funcsOf(t, src))
	if len(res.Checks) != 1 || res.Checks[0].Name != "bench-regression" {
		t.Fatalf("want a regression: %+v notes=%v", res.Checks, res.Notes)
	}
	if !strings.Contains(res.Checks[0].Lines[0], "BenchmarkSum  time/op  +") {
		t.Errorf("lines = %v", res.Checks[0].Lines)
	}
	if out := git("worktree", "list"); strings.Count(out, "\n") != 0 {
		t.Errorf("worktree not cleaned up:\n%s", out)
	}
}
