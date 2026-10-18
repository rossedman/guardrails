package testdepth

import (
	"context"
	"errors"
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

// fuzzFixture is a repo with one package directory, pkg.
type fuzzFixture struct {
	t    *testing.T
	repo string
	pkg  string
	fake *runner.Fake
	cfg  config.Config
}

func newFuzzFixture(t *testing.T) *fuzzFixture {
	t.Helper()
	repo := t.TempDir()
	pkg := filepath.Join(repo, "internal", "codec")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Go.Fuzz.BudgetSeconds = 0
	return &fuzzFixture{t: t, repo: repo, pkg: pkg, fake: runner.NewFake("go"), cfg: cfg}
}

func (f *fuzzFixture) write(name, src string) string {
	f.t.Helper()
	p := filepath.Join(f.pkg, name)
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		f.t.Fatal(err)
	}
	return p
}

func (f *fuzzFixture) env(asked map[string]bool) Env {
	return Env{
		Env:   module.Env{Runner: f.fake, Config: f.cfg, Repo: f.repo},
		Root:  f.repo,
		Asked: asked,
		Now:   time.Now,
	}
}

// funcs returns every function in path as changed.
func (f *fuzzFixture) funcs(paths ...string) []Func {
	f.t.Helper()
	changed := map[string]coverage.LineSet{}
	for _, p := range paths {
		set := coverage.LineSet{}
		for i := 1; i < 200; i++ {
			set[i] = true
		}
		changed[p] = set
	}
	fs, err := ChangedFuncs(changed)
	if err != nil {
		f.t.Fatal(err)
	}
	return fs
}

const fuzzSource = `package codec

import "errors"

func ParseConfig(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, errors.New("empty")
	}
	return len(b), nil
}

func Load(s string) int { return len(s) }

func Covered(s string) (int, error) { return len(s), nil }

// guardrails:nofuzz only fed by trusted config
func ParseSkip(b []byte) (int, error) { return len(b), nil }

func Add(a, b int) int { return a + b }
`

const fuzzTests = `package codec

import "testing"

func FuzzCovered(f *testing.F) {
	f.Add("a")
	f.Fuzz(func(t *testing.T, s string) { _, _ = Covered(s) })
}
`

func TestFuzzRequirements(t *testing.T) {
	f := newFuzzFixture(t)
	src := f.write("codec.go", fuzzSource)
	f.write("codec_test.go", fuzzTests)
	funcs := f.funcs(src)

	res := Fuzz(context.Background(), f.env(nil), funcs, nil)
	c := findFuzzCheck(res, "fuzz-targets")
	if c == nil {
		t.Fatalf("no fuzz-targets check: %+v", res)
	}
	if c.Summary != "2 functions that parse untrusted input have no fuzz target" {
		t.Errorf("summary = %q", c.Summary)
	}
	if len(c.Lines) != 2 {
		t.Fatalf("lines = %q", c.Lines)
	}
	if !strings.HasPrefix(c.Lines[0], "internal/codec/codec.go:5  ParseConfig  needs a Fuzz target (parses []byte input and can fail)") {
		t.Errorf("strong line = %q", c.Lines[0])
	}
	if strings.Contains(c.Lines[0], "why not") {
		t.Errorf("strong requirement should not offer an explanation: %q", c.Lines[0])
	}
	if !strings.Contains(c.Lines[1], "Load") || !strings.HasSuffix(c.Lines[1], " — or say in one line why not") {
		t.Errorf("weak line = %q", c.Lines[1])
	}
	if !strings.Contains(c.Fix, "guardrails:nofuzz") {
		t.Errorf("fix = %q", c.Fix)
	}
	wantKey := "fuzz:internal/codec.Load"
	if len(res.Asked) != 1 || res.Asked[0] != wantKey {
		t.Errorf("asked = %v", res.Asked)
	}
	if !hasFuzz(res.Ran, "fuzz-targets") || !hasFuzz(res.Ran, "fuzz") {
		t.Errorf("ran = %v", res.Ran)
	}
	// The covering target ran.
	if !f.fake.Ran("go test -run ^$ -fuzz ^FuzzCovered$ -fuzztime 10s ./internal/codec") {
		t.Errorf("calls = %v", f.fake.Calls())
	}

	// Asked once: the borderline requirement is not raised again; the strong
	// one still blocks.
	res = Fuzz(context.Background(), f.env(map[string]bool{wantKey: true}), funcs, nil)
	c = findFuzzCheck(res, "fuzz-targets")
	if c == nil || len(c.Lines) != 1 || c.Summary != "1 function that parse untrusted input have no fuzz target" {
		t.Fatalf("second run: %+v", c)
	}
	if len(res.Asked) != 0 {
		t.Errorf("asked again: %v", res.Asked)
	}
}

func TestFuzzRequirementsOnlyBorderlineAlreadyAsked(t *testing.T) {
	f := newFuzzFixture(t)
	src := f.write("load.go", "package codec\n\nfunc Load(s string) int { return len(s) }\n")
	res := Fuzz(context.Background(), f.env(map[string]bool{"fuzz:internal/codec.Load": true}), f.funcs(src), nil)
	if len(res.Checks) != 0 || len(res.Asked) != 0 {
		t.Fatalf("res = %+v", res)
	}
	if !hasFuzz(res.Ran, "fuzz-targets") || hasFuzz(res.Ran, "fuzz") {
		t.Errorf("ran = %v", res.Ran)
	}
}

func TestFuzzRequirementsDisabled(t *testing.T) {
	f := newFuzzFixture(t)
	f.cfg.Go.Fuzz.RequireOnParsers = false
	src := f.write("codec.go", fuzzSource)
	res := Fuzz(context.Background(), f.env(nil), f.funcs(src), nil)
	if findFuzzCheck(res, "fuzz-targets") != nil || hasFuzz(res.Ran, "fuzz-targets") {
		t.Fatalf("res = %+v", res)
	}
}

func TestFuzzTargetsFromTouchedTestFiles(t *testing.T) {
	f := newFuzzFixture(t)
	src := f.write("codec.go", fuzzSource)
	testFile := f.write("codec_test.go", fuzzTests)
	other := f.write("other_test.go", `package codec

import "testing"

func FuzzOther(f *testing.F) { f.Fuzz(func(t *testing.T, s string) {}) }

func TestPlain(t *testing.T) {}
`)
	// A second package with a target at the module root.
	if err := os.WriteFile(filepath.Join(f.repo, "root_test.go"), []byte(`package root

import "testing"

func FuzzRoot(f *testing.F) { f.Fuzz(func(t *testing.T, s string) {}) }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	touched := []string{
		testFile, other, // FuzzCovered also covers a changed func: deduped
		filepath.Join(f.repo, "root_test.go"),
		src,                                     // not a test file
		filepath.Join(f.repo, "gone_test.go"),   // deleted
		filepath.Join(f.repo, "x", "y_test.go"), // directory missing
	}
	f.cfg.Go.Fuzz.RequireOnParsers = false
	res := Fuzz(context.Background(), f.env(nil), f.funcs(src), touched)
	if len(res.Checks) != 0 {
		t.Fatalf("checks = %+v", res.Checks)
	}
	var runs []string
	for _, c := range f.fake.Calls() {
		runs = append(runs, c.String())
	}
	want := []string{
		"go test -run ^$ -fuzz ^FuzzRoot$ -fuzztime 10s .",
		"go test -run ^$ -fuzz ^FuzzCovered$ -fuzztime 10s ./internal/codec",
		"go test -run ^$ -fuzz ^FuzzOther$ -fuzztime 10s ./internal/codec",
	}
	if strings.Join(runs, "\n") != strings.Join(want, "\n") {
		t.Errorf("runs:\n%s\nwant:\n%s", strings.Join(runs, "\n"), strings.Join(want, "\n"))
	}
	for _, c := range f.fake.Calls() {
		if c.Dir != f.repo {
			t.Errorf("ran in %s, want %s", c.Dir, f.repo)
		}
	}
}

func TestFuzzNoTargetsNoRuns(t *testing.T) {
	f := newFuzzFixture(t)
	src := f.write("add.go", "package codec\n\nfunc Add(a, b int) int { return a + b }\n")
	res := Fuzz(context.Background(), f.env(nil), f.funcs(src), nil)
	if len(f.fake.Calls()) != 0 || hasFuzz(res.Ran, "fuzz") {
		t.Fatalf("res = %+v calls = %v", res, f.fake.Calls())
	}
	// No changed funcs at all: requirements are not evaluated.
	res = Fuzz(context.Background(), f.env(nil), nil, nil)
	if len(res.Ran) != 0 {
		t.Errorf("ran = %v", res.Ran)
	}
}

func TestFuzzUnparseableTestFileIsIgnored(t *testing.T) {
	f := newFuzzFixture(t)
	src := f.write("codec.go", fuzzSource)
	bad := f.write("bad_test.go", "package codec\n\nfunc FuzzBroken(\n")
	res := Fuzz(context.Background(), f.env(nil), f.funcs(src), []string{bad})
	// Without an index, every parser lacks a target, including Covered.
	c := findFuzzCheck(res, "fuzz-targets")
	if c == nil || len(c.Lines) != 3 {
		t.Fatalf("check = %+v", c)
	}
	if len(f.fake.Calls()) != 0 {
		t.Errorf("calls = %v", f.fake.Calls())
	}
}

func TestFuzzCrashes(t *testing.T) {
	cases := []struct {
		name    string
		out     runner.Result
		summary string
		line    string
	}{
		{
			name: "panic",
			out: runner.Result{ExitCode: 1, Stdout: `fuzz: elapsed: 0s, gathering baseline coverage: 0/1 completed
--- FAIL: FuzzCovered (0.03s)
    --- FAIL: FuzzCovered (0.00s)
        testing.go:1591: panic: runtime error: index out of range [3] with length 3
        goroutine 7 [running]:

    Failing input written to testdata/fuzz/FuzzCovered/9f3c2a
    To re-run:
    go test -run=FuzzCovered/9f3c2a
FAIL`},
			summary: "1 fuzz target found a failing input",
			line:    "FuzzCovered  panic: runtime error: index out of range [3] with length 3  →  internal/codec/testdata/fuzz/FuzzCovered/9f3c2a (now a regression test)",
		},
		{
			name: "t.Fatal",
			out: runner.Result{ExitCode: 1, Stdout: `--- FAIL: FuzzCovered (0.03s)
    --- FAIL: FuzzCovered (0.00s)
        codec_test.go:12: round trip mismatch: "a" != "b"

    Failing input written to testdata/fuzz/FuzzCovered/abc
FAIL`},
			summary: "1 fuzz target found a failing input",
			line:    `FuzzCovered  round trip mismatch: "a" != "b"  →  internal/codec/testdata/fuzz/FuzzCovered/abc (now a regression test)`,
		},
		{
			name:    "no message",
			out:     runner.Result{ExitCode: 1, Stderr: "Failing input written to testdata/fuzz/FuzzCovered/zz\n"},
			summary: "1 fuzz target found a failing input",
			line:    "FuzzCovered  failing input  →  internal/codec/testdata/fuzz/FuzzCovered/zz (now a regression test)",
		},
		{
			name:    "build error",
			out:     runner.Result{ExitCode: 1, Stderr: "# example.com/codec\n\n./codec.go:3:1: syntax error\nFAIL\n"},
			summary: "fuzz targets failed to run",
			line:    "FuzzCovered  failed before fuzzing:",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFuzzFixture(t)
			f.cfg.Go.Fuzz.RequireOnParsers = false
			src := f.write("codec.go", fuzzSource)
			f.write("codec_test.go", fuzzTests)
			f.fake.On("go test", tc.out)
			res := Fuzz(context.Background(), f.env(nil), f.funcs(src), nil)
			c := findFuzzCheck(res, "fuzz")
			if c == nil {
				t.Fatalf("no fuzz check: %+v", res)
			}
			if c.Summary != tc.summary || c.Lines[0] != tc.line {
				t.Errorf("got %q / %q", c.Summary, c.Lines[0])
			}
			if !strings.Contains(c.Fix, "regression test") {
				t.Errorf("fix = %q", c.Fix)
			}
			if tc.name == "build error" && (len(c.Lines) < 2 || !strings.Contains(strings.Join(c.Lines, "\n"), "syntax error")) {
				t.Errorf("lines = %q", c.Lines)
			}
		})
	}
}

func TestFuzzSeveralCrashes(t *testing.T) {
	f := newFuzzFixture(t)
	f.cfg.Go.Fuzz.RequireOnParsers = false
	src := f.write("codec.go", fuzzSource+"\nfunc Second(s string) int { return Covered2(s) }\n\nfunc Covered2(s string) int { return 0 }\n")
	f.write("codec_test.go", fuzzTests+`
func FuzzSecond(f *testing.F) { f.Fuzz(func(t *testing.T, s string) { Covered2(s) }) }
`)
	f.fake.On("go test", runner.Result{ExitCode: 1, Stdout: "panic: boom\nFailing input written to testdata/fuzz/FuzzX/1\n"})
	res := Fuzz(context.Background(), f.env(nil), f.funcs(src), nil)
	c := findFuzzCheck(res, "fuzz")
	if c == nil || c.Summary != "2 fuzz targets found failing inputs" || len(c.Lines) != 2 {
		t.Fatalf("check = %+v", c)
	}
}

func TestFuzzRunErrorIsANote(t *testing.T) {
	f := newFuzzFixture(t)
	f.cfg.Go.Fuzz.RequireOnParsers = false
	src := f.write("codec.go", fuzzSource)
	f.write("codec_test.go", fuzzTests)
	f.fake.OnError("go test", errors.New("signal: killed"))
	res := Fuzz(context.Background(), f.env(nil), f.funcs(src), nil)
	if len(res.Checks) != 0 || len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "FuzzCovered did not finish: signal: killed") {
		t.Fatalf("res = %+v", res)
	}
}

func TestFuzzBudgetSkipsRemainingTargets(t *testing.T) {
	f := newFuzzFixture(t)
	f.cfg.Go.Fuzz.RequireOnParsers = false
	f.cfg.Go.Fuzz.BudgetSeconds = 1
	f.cfg.Go.Fuzz.Fuzztime = "not-a-duration"
	src := f.write("codec.go", fuzzSource)
	f.write("codec_test.go", fuzzTests+`
func FuzzA(f *testing.F) { f.Fuzz(func(t *testing.T, s string) { _, _ = Covered(s) }) }

func FuzzB(f *testing.F) { f.Fuzz(func(t *testing.T, s string) { _, _ = Covered(s) }) }
`)
	f.fake.OnFunc("go test", func(runner.Call) (runner.Result, error) {
		time.Sleep(1100 * time.Millisecond)
		return runner.Result{}, nil
	})
	res := Fuzz(context.Background(), f.env(nil), f.funcs(src), nil)
	calls := f.fake.Calls()
	if len(calls) != 1 || !strings.Contains(calls[0].String(), "-fuzz ^FuzzA$ -fuzztime 10s") {
		t.Fatalf("calls = %v", calls)
	}
	if len(res.Notes) != 1 || res.Notes[0] != "fuzz: skipped FuzzB, FuzzCovered (1s budget used)" {
		t.Errorf("notes = %q", res.Notes)
	}
	if !hasFuzz(res.Ran, "fuzz") {
		t.Errorf("ran = %v", res.Ran)
	}
}

func TestFuzzHelpers(t *testing.T) {
	if got := fuzzRel("/repo", "/elsewhere/x.go"); got != "/elsewhere/x.go" {
		t.Errorf("fuzzRel outside = %q", got)
	}
	if got := fuzzPkg("rel", "/abs"); got != "/abs" {
		t.Errorf("fuzzPkg mixed = %q", got)
	}
	if got := fuzzFirstLines("a\n\nb\nc\n", 2); strings.Join(got, ",") != "a,b" {
		t.Errorf("fuzzFirstLines = %q", got)
	}
}

func TestFuzzIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the real fuzzer")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not installed")
	}
	repo := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/fz\n\ngo 1.22\n",
		"parse.go": `package fz

import "strconv"

// Parse panics on inputs starting with x, a bug for the fuzzer to find.
func Parse(s string) (int, error) {
	if len(s) > 2 && s[0] == 'x' {
		var a []int
		return a[len(s)], nil
	}
	return strconv.Atoi(s)
}
`,
		"parse_test.go": `package fz

import "testing"

func FuzzParse(f *testing.F) {
	f.Add("a")
	f.Fuzz(func(t *testing.T, s string) { _, _ = Parse(s) })
}
`,
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default()
	cfg.Go.Fuzz.Fuzztime = "5s"
	cfg.Go.Fuzz.BudgetSeconds = 120
	e := Env{Env: module.Env{Runner: runner.Exec{}, Config: cfg, Repo: repo}, Root: repo, Now: time.Now}
	src := filepath.Join(repo, "parse.go")
	set := coverage.LineSet{}
	for i := 1; i < 20; i++ {
		set[i] = true
	}
	funcs, err := ChangedFuncs(map[string]coverage.LineSet{src: set})
	if err != nil {
		t.Fatal(err)
	}
	res := Fuzz(context.Background(), e, funcs, nil)
	c := findFuzzCheck(res, "fuzz")
	if c == nil || c.Summary != "1 fuzz target found a failing input" {
		t.Fatalf("no crash found: %+v", res)
	}
	if !strings.Contains(c.Lines[0], "panic: runtime error: index out of range") || !strings.Contains(c.Lines[0], "testdata/fuzz/FuzzParse/") {
		t.Errorf("line = %q", c.Lines[0])
	}
	saved, err := filepath.Glob(filepath.Join(repo, "testdata", "fuzz", "FuzzParse", "*"))
	if err != nil || len(saved) == 0 {
		t.Errorf("failing input not saved: %v %v", saved, err)
	}
	// Parse has a covering target, so there is no requirement check.
	if findFuzzCheck(res, "fuzz-targets") != nil {
		t.Errorf("unexpected requirement: %+v", res.Checks)
	}
}

func findFuzzCheck(res Result, name string) *struct {
	Summary string
	Lines   []string
	Fix     string
} {
	for _, c := range res.Checks {
		if c.Name == name {
			return &struct {
				Summary string
				Lines   []string
				Fix     string
			}{c.Summary, c.Lines, c.Fix}
		}
	}
	return nil
}

func hasFuzz(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
