package golang

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rossedman/guardrails/internal/config"
	"github.com/rossedman/guardrails/internal/feedback"
	"github.com/rossedman/guardrails/internal/module"
	"github.com/rossedman/guardrails/internal/runner"
)

const modPath = "example.com/demo"

// aSource has one covered function (lines 3-5) and one uncovered function
// with three statements (lines 7-12).
const aSource = `package a

func Covered() int {
	return 1
}

func Uncovered(x int) int {
	if x > 0 {
		return x
	}
	return -x
}
`

const bSource = `package b

func B() int {
	return 2
}
`

// writeRepo creates a module in a temp dir with the given files.
func writeRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	all := map[string]string{"go.mod": "module " + modPath + "\n\ngo 1.24\n"}
	for k, v := range files {
		all[k] = v
	}
	for rel, content := range all {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func newEnv(t *testing.T, repo string, f *runner.Fake) module.Env {
	t.Helper()
	return module.Env{Runner: f, Config: config.Default(), Repo: repo, DataDir: t.TempDir()}
}

// profileWriter returns an OnFunc that writes profile to the -coverprofile
// path of the go test call.
func profileWriter(profile string) func(runner.Call) (runner.Result, error) {
	return func(c runner.Call) (runner.Result, error) {
		for _, a := range c.Args {
			if p, ok := strings.CutPrefix(a, "-coverprofile="); ok {
				if err := os.WriteFile(p, []byte(profile), 0o600); err != nil {
					return runner.Result{}, err
				}
			}
		}
		return runner.Result{Stdout: "ok"}, nil
	}
}

const aProfileLow = "mode: atomic\n" +
	modPath + "/a/a.go:3.20,5.2 1 1\n" +
	modPath + "/a/a.go:7.27,8.11 1 0\n" +
	modPath + "/a/a.go:8.11,10.3 1 0\n" +
	modPath + "/a/a.go:11.2,11.11 1 0\n"

const aProfileFull = "mode: atomic\n" +
	modPath + "/a/a.go:3.20,5.2 1 1\n" +
	modPath + "/a/a.go:7.27,8.11 1 1\n" +
	modPath + "/a/a.go:8.11,10.3 1 1\n" +
	modPath + "/a/a.go:11.2,11.11 1 1\n"

func fakeAll() *runner.Fake {
	return runner.NewFake("go", "golangci-lint", "govulncheck", "goimports", "gofumpt")
}

func findCheck(checks []feedback.Check, name string) (feedback.Check, bool) {
	for _, c := range checks {
		if c.Name == name {
			return c, true
		}
	}
	return feedback.Check{}, false
}

func checkNames(checks []feedback.Check) []string {
	var out []string
	for _, c := range checks {
		out = append(out, c.Name)
	}
	return out
}

func TestModuleBasics(t *testing.T) {
	t.Parallel()
	m := New()
	if m.Name() != "go" {
		t.Errorf("Name() = %q", m.Name())
	}
	cfg := config.Default()
	if !m.Enabled(cfg) {
		t.Error("enabled by default")
	}
	off := false
	cfg.Go.Enabled = &off
	if m.Enabled(cfg) {
		t.Error("disabled when enabled=false")
	}
	for path, want := range map[string]bool{
		"/x/a.go": true, "/x/a_test.go": true, "/x/go.mod": true,
		"/x/go.sum": false, "/x/a.py": false, "/x/main.gox": false,
	} {
		if got := m.Match(path); got != want {
			t.Errorf("Match(%q) = %v, want %v", path, got, want)
		}
	}
	tools := m.Tools()
	var required []string
	for _, tl := range tools {
		if tl.Required {
			required = append(required, tl.Name)
		}
		if tl.Install == "" {
			t.Errorf("tool %s has no install hint", tl.Name)
		}
	}
	if !slices.Equal(required, []string{"go", "golangci-lint"}) {
		t.Errorf("required tools = %v", required)
	}
}

func TestGateGoMissing(t *testing.T) {
	t.Parallel()
	repo := writeRepo(t, map[string]string{"a/a.go": aSource})
	f := runner.NewFake()
	res := New().Gate(context.Background(), newEnv(t, repo, f), module.GateInput{Files: []string{filepath.Join(repo, "a/a.go")}})
	c, ok := findCheck(res.Checks, "tools")
	if !ok || !c.Failed || !strings.Contains(c.Summary, "go toolchain") {
		t.Fatalf("want tools check, got %+v", res.Checks)
	}
	if res.Total != -1 {
		t.Errorf("Total = %v, want -1", res.Total)
	}
}

func TestGateBuildFailure(t *testing.T) {
	t.Parallel()
	repo := writeRepo(t, map[string]string{"a/a.go": aSource})
	f := fakeAll().On("go build", runner.Result{ExitCode: 1, Stderr: "# example.com/demo/a\na/a.go:4:9: undefined: y\n"})
	res := New().Gate(context.Background(), newEnv(t, repo, f), module.GateInput{Files: []string{filepath.Join(repo, "a/a.go")}})
	c, ok := findCheck(res.Checks, "build")
	if !ok {
		t.Fatalf("want build check, got %v", checkNames(res.Checks))
	}
	if len(c.Issues) != 1 || c.Issues[0].File != "a/a.go" || c.Issues[0].Line != 4 || c.Issues[0].Rule != "compile" {
		t.Errorf("issues = %+v", c.Issues)
	}
	if len(c.Lines) != 0 {
		t.Errorf("lines should be empty when issues parsed: %v", c.Lines)
	}
	if f.Ran("go test") {
		t.Error("tests must not run after a build failure")
	}
	if !slices.Equal(res.Ran, []string{"build"}) {
		t.Errorf("Ran = %v", res.Ran)
	}
}

func TestGateBuildFailureUnparsed(t *testing.T) {
	t.Parallel()
	repo := writeRepo(t, map[string]string{"a/a.go": aSource})
	f := fakeAll().On("go build", runner.Result{ExitCode: 1, Stderr: "go: cannot find main module\n"})
	res := New().Gate(context.Background(), newEnv(t, repo, f), module.GateInput{Files: []string{filepath.Join(repo, "a/a.go")}})
	c, _ := findCheck(res.Checks, "build")
	if len(c.Lines) != 1 || !strings.Contains(c.Lines[0], "cannot find main module") {
		t.Errorf("lines = %v", c.Lines)
	}
}

func TestGateBuildStartError(t *testing.T) {
	t.Parallel()
	repo := writeRepo(t, map[string]string{"a/a.go": aSource})
	f := fakeAll().OnError("go build", errors.New("boom"))
	res := New().Gate(context.Background(), newEnv(t, repo, f), module.GateInput{Files: []string{filepath.Join(repo, "a/a.go")}})
	c, ok := findCheck(res.Checks, "build")
	if !ok || !strings.Contains(c.Summary, "boom") {
		t.Fatalf("got %+v", res.Checks)
	}
}

func TestGateBadGoMod(t *testing.T) {
	t.Parallel()
	repo := writeRepo(t, map[string]string{"a/a.go": aSource})
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("go 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := New().Gate(context.Background(), newEnv(t, repo, fakeAll()), module.GateInput{Files: []string{filepath.Join(repo, "a/a.go")}})
	c, ok := findCheck(res.Checks, "build")
	if !ok || !strings.Contains(c.Summary, "no module directive") {
		t.Fatalf("got %+v", res.Checks)
	}
}

func TestGateTestFailure(t *testing.T) {
	t.Parallel()
	repo := writeRepo(t, map[string]string{"a/a.go": aSource})
	out := "--- FAIL: TestUncovered (0.00s)\n    a_test.go:9: got 1 want 2\nFAIL\n"
	f := fakeAll().On("go test", runner.Result{ExitCode: 1, Stdout: out})
	res := New().Gate(context.Background(), newEnv(t, repo, f), module.GateInput{Files: []string{filepath.Join(repo, "a/a.go")}})
	c, ok := findCheck(res.Checks, "tests")
	if !ok {
		t.Fatalf("want tests check, got %v", checkNames(res.Checks))
	}
	if c.Summary != "1 failing test: TestUncovered" {
		t.Errorf("summary = %q", c.Summary)
	}
	if len(c.Issues) != 1 || c.Issues[0].Line != 9 {
		t.Errorf("issues = %+v", c.Issues)
	}
	if _, ok := findCheck(res.Checks, "coverage"); ok {
		t.Error("coverage must not be reported when tests fail")
	}
	if res.Total != -1 {
		t.Errorf("Total = %v, want -1 (not measured)", res.Total)
	}
}

func TestGateTestFailureNoLocations(t *testing.T) {
	t.Parallel()
	repo := writeRepo(t, map[string]string{"a/a.go": aSource})
	var b strings.Builder
	for i := range 10 {
		b.WriteString("--- FAIL: TestN" + string(rune('A'+i)) + " (0.00s)\n")
	}
	b.WriteString("panic: oh no\n")
	f := fakeAll().On("go test", runner.Result{ExitCode: 1, Stdout: b.String()})
	res := New().Gate(context.Background(), newEnv(t, repo, f), module.GateInput{Files: []string{filepath.Join(repo, "a/a.go")}})
	c, _ := findCheck(res.Checks, "tests")
	if !strings.HasPrefix(c.Summary, "10 failing tests: ") || !strings.HasSuffix(c.Summary, "+2 more") {
		t.Errorf("summary = %q", c.Summary)
	}
	if len(c.Lines) == 0 || c.Lines[len(c.Lines)-1] != "panic: oh no" {
		t.Errorf("lines = %v", c.Lines)
	}
}

func TestGateTestErrors(t *testing.T) {
	t.Parallel()
	repo := writeRepo(t, map[string]string{"a/a.go": aSource})
	file := filepath.Join(repo, "a/a.go")

	f := fakeAll().OnError("go test", errors.New("killed"))
	res := New().Gate(context.Background(), newEnv(t, repo, f), module.GateInput{Files: []string{file}})
	if c, ok := findCheck(res.Checks, "tests"); !ok || !strings.Contains(c.Summary, "killed") {
		t.Errorf("start error: %+v", res.Checks)
	}

	// go test succeeds but writes a malformed profile.
	f = fakeAll().OnFunc("go test", profileWriter("mode: set\ngarbage\n"))
	res = New().Gate(context.Background(), newEnv(t, repo, f), module.GateInput{Files: []string{file}})
	if c, ok := findCheck(res.Checks, "coverage"); !ok || !strings.Contains(c.Summary, "could not run") {
		t.Errorf("bad profile: %+v", res.Checks)
	}
}

func TestGateCoverageBelowTotal(t *testing.T) {
	t.Parallel()
	repo := writeRepo(t, map[string]string{"a/a.go": aSource})
	f := fakeAll().OnFunc("go test", profileWriter(aProfileLow))
	env := newEnv(t, repo, f)
	res := New().Gate(context.Background(), env, module.GateInput{Files: []string{filepath.Join(repo, "a/a.go")}})

	c, ok := findCheck(res.Checks, "coverage")
	if !ok {
		t.Fatalf("want coverage check, got %v", checkNames(res.Checks))
	}
	if c.Summary != "25.0% < 90.0% (touched: ./a)" {
		t.Errorf("summary = %q", c.Summary)
	}
	want := "a/a.go:7  Uncovered  0/3 stmts"
	if !slices.Contains(c.Lines, want) {
		t.Errorf("lines = %q, want %q", c.Lines, want)
	}
	if res.Total != 25 || res.Coverage[modPath+"/a"] != 25 {
		t.Errorf("Total = %v, Coverage = %v", res.Total, res.Coverage)
	}
	// -race is on by default, -count=1 always.
	var testCall runner.Call
	for _, call := range f.Calls() {
		if strings.HasPrefix(call.String(), "go test") {
			testCall = call
		}
	}
	if !slices.Contains(testCall.Args, "-race") || !slices.Contains(testCall.Args, "-count=1") || !slices.Contains(testCall.Args, "./a") {
		t.Errorf("go test args = %v", testCall.Args)
	}
	for _, r := range []string{"build", "tests", "coverage", "lint", "mod-tidy", "vuln"} {
		if !slices.Contains(res.Ran, r) {
			t.Errorf("Ran = %v, missing %s", res.Ran, r)
		}
	}
}

func TestGateCoveragePasses(t *testing.T) {
	t.Parallel()
	repo := writeRepo(t, map[string]string{"a/a.go": aSource})
	f := fakeAll().OnFunc("go test", profileWriter(aProfileFull))
	env := newEnv(t, repo, f)
	env.Config.Go.Race = false
	res := New().Gate(context.Background(), env, module.GateInput{Files: []string{filepath.Join(repo, "a/a.go")}})
	if len(res.Checks) != 0 {
		t.Fatalf("want no failing checks, got %+v", res.Checks)
	}
	if res.Total != 100 {
		t.Errorf("Total = %v", res.Total)
	}
	for _, call := range f.Calls() {
		if strings.HasPrefix(call.String(), "go test") && slices.Contains(call.Args, "-race") {
			t.Error("-race passed although race is off")
		}
	}
}

func TestGatePerPackageFloorAndRatchet(t *testing.T) {
	t.Parallel()
	repo := writeRepo(t, map[string]string{"a/a.go": aSource, "b/b.go": bSource})
	// b fully covered with many statements so the total passes; a is at
	// 25% and below the floor.
	profile := aProfileLow + modPath + "/b/b.go:3.14,5.2 40 1\n"
	f := fakeAll().OnFunc("go test", profileWriter(profile))
	env := newEnv(t, repo, f)
	res := New().Gate(context.Background(), env, module.GateInput{
		Files:    []string{filepath.Join(repo, "a/a.go"), filepath.Join(repo, "b/b.go")},
		Baseline: map[string]float64{modPath + "/a": 50, modPath + "/b": 100},
	})
	c, ok := findCheck(res.Checks, "coverage")
	if !ok {
		t.Fatalf("want coverage check, got %v", checkNames(res.Checks))
	}
	joined := strings.Join(c.Lines, "\n")
	if !strings.Contains(joined, modPath+"/a  25.0% below the per-package floor of 80%") {
		t.Errorf("missing floor line:\n%s", joined)
	}
	if !strings.Contains(joined, modPath+"/a  25.0% dropped below its session baseline of 50.0%") {
		t.Errorf("missing ratchet line:\n%s", joined)
	}
	if strings.Contains(joined, modPath+"/b ") {
		t.Errorf("b should pass:\n%s", joined)
	}
	if !strings.HasPrefix(c.Summary, "93.2% total (touched: ./a, ./b)") {
		t.Errorf("summary = %q", c.Summary)
	}

	// With the ratchet off, only the floor fires.
	env.Config.Go.Coverage.Ratchet = false
	res = New().Gate(context.Background(), env, module.GateInput{
		Files:    []string{filepath.Join(repo, "a/a.go"), filepath.Join(repo, "b/b.go")},
		Baseline: map[string]float64{modPath + "/a": 50},
	})
	c, _ = findCheck(res.Checks, "coverage")
	if strings.Contains(strings.Join(c.Lines, "\n"), "baseline") {
		t.Errorf("ratchet off but got %v", c.Lines)
	}
}

func TestGateExclusions(t *testing.T) {
	t.Parallel()
	gen := "// Code generated by tool. DO NOT EDIT.\n\npackage a\n\nfunc G() int {\n\treturn 1\n}\n"
	repo := writeRepo(t, map[string]string{
		"a/a.go":           aSource,
		"a/zz_gen.go":      gen,
		"a/mocks/m.go":     "package mocks\n",
		"cmd/tool/main.go": "package main\n\nfunc main() {}\n",
	})
	profile := aProfileFull +
		modPath + "/a/zz_gen.go:5.14,7.2 50 0\n" +
		modPath + "/a/mocks/m.go:1.1,1.2 50 0\n" +
		modPath + "/cmd/tool/main.go:3.13,3.15 50 0\n" +
		"other.com/x/y.go:1.1,2.2 1 1\n"
	f := fakeAll().OnFunc("go test", profileWriter(profile))
	res := New().Gate(context.Background(), newEnv(t, repo, f), module.GateInput{Files: []string{filepath.Join(repo, "a/a.go")}})
	if _, ok := findCheck(res.Checks, "coverage"); ok {
		t.Fatalf("excluded files must not count: %+v", res.Checks)
	}
	if _, ok := res.Coverage[modPath+"/a/mocks"]; ok {
		t.Error("mocks package should be excluded")
	}
	if _, ok := res.Coverage["other.com/x"]; !ok {
		t.Error("unresolvable files are kept")
	}
}

func TestGatePatchCoverage(t *testing.T) {
	t.Parallel()
	newFile := "package a\n\nfunc New() int {\n\treturn 3\n}\n"
	repo := writeRepo(t, map[string]string{"a/a.go": aSource, "a/new.go": newFile, "a/a_test.go": "package a\n"})
	profile := aProfileLow + modPath + "/a/new.go:3.16,5.2 1 0\n"
	diffOut := "diff --git a/a/a.go b/a/a.go\n--- a/a/a.go\n+++ b/a/a.go\n@@ -8,0 +8,4 @@\n+x\n+y\n+z\n+w\n"
	f := fakeAll().
		OnFunc("go test", profileWriter(profile)).
		On("git diff", runner.Result{Stdout: diffOut}).
		On("git ls-files", runner.Result{Stdout: "a/new.go\n"})
	env := newEnv(t, repo, f)
	env.Config.Go.Coverage.Total = 0
	env.Config.Go.Coverage.PerPackageFloor = 0
	res := New().Gate(context.Background(), env, module.GateInput{
		Files:      []string{filepath.Join(repo, "a/a.go"), filepath.Join(repo, "a/new.go"), filepath.Join(repo, "a/a_test.go")},
		BaseCommit: "abc123",
	})
	c, ok := findCheck(res.Checks, "patch-coverage")
	if !ok {
		t.Fatalf("want patch-coverage, got %v", checkNames(res.Checks))
	}
	if !strings.HasPrefix(c.Summary, "0.0% of changed lines covered < 90.0%") {
		t.Errorf("summary = %q", c.Summary)
	}
	wantLines := []string{"a/a.go:8-11  Uncovered", "a/new.go:3-5  New"}
	if !slices.Equal(c.Lines, wantLines) {
		t.Errorf("lines = %q, want %q", c.Lines, wantLines)
	}
	if !slices.Contains(res.Ran, "patch-coverage") {
		t.Errorf("Ran = %v", res.Ran)
	}
	// The diff only names non-test files.
	for _, call := range f.Calls() {
		if call.Name == "git" && call.Args[0] == "diff" {
			if !slices.Contains(call.Args, "abc123") || slices.Contains(call.Args, filepath.Join(repo, "a/a_test.go")) {
				t.Errorf("git diff args = %v", call.Args)
			}
		}
	}
}

func TestGatePatchCoverageSkipped(t *testing.T) {
	t.Parallel()
	repo := writeRepo(t, map[string]string{"a/a.go": aSource, "a/a_test.go": "package a\n"})
	file := filepath.Join(repo, "a/a.go")

	// git diff fails: nothing to measure.
	f := fakeAll().OnFunc("go test", profileWriter(aProfileFull)).On("git diff", runner.Result{ExitCode: 128})
	res := New().Gate(context.Background(), newEnv(t, repo, f), module.GateInput{Files: []string{file}, BaseCommit: "abc"})
	if slices.Contains(res.Ran, "patch-coverage") {
		t.Error("patch coverage ran despite git failure")
	}

	// Only test files touched: no diff is taken.
	f = fakeAll().OnFunc("go test", profileWriter(aProfileFull))
	_ = New().Gate(context.Background(), newEnv(t, repo, f), module.GateInput{Files: []string{filepath.Join(repo, "a/a_test.go")}, BaseCommit: "abc"})
	if f.Ran("git diff") {
		t.Error("git diff should not run with only test files")
	}

	// Covered changed lines pass.
	diffOut := "+++ b/a/a.go\n@@ -1,0 +8,1 @@\n+x\n"
	f = fakeAll().OnFunc("go test", profileWriter(aProfileFull)).On("git diff", runner.Result{Stdout: diffOut})
	res = New().Gate(context.Background(), newEnv(t, repo, f), module.GateInput{Files: []string{file}, BaseCommit: "abc"})
	if _, ok := findCheck(res.Checks, "patch-coverage"); ok {
		t.Errorf("covered patch should pass: %+v", res.Checks)
	}
}

const lintJSON = `{"Issues":[` +
	`{"FromLinter":"errcheck","Text":"Error return value is not checked","Pos":{"Filename":"a/a.go","Line":4,"Column":2}},` +
	`{"FromLinter":"revive","Text":"exported thing","Pos":{"Filename":"b/b.go","Line":3,"Column":1}}` +
	`],"Report":{}}`

func TestGateLint(t *testing.T) {
	t.Parallel()
	repo := writeRepo(t, map[string]string{"a/a.go": aSource, "b/b.go": bSource})
	f := fakeAll().
		OnFunc("go test", profileWriter(aProfileFull)).
		On("golangci-lint", runner.Result{ExitCode: 1, Stdout: lintJSON + "\n2 issues.\n"})
	res := New().Gate(context.Background(), newEnv(t, repo, f), module.GateInput{Files: []string{filepath.Join(repo, "a/a.go")}})
	c, ok := findCheck(res.Checks, "lint")
	if !ok {
		t.Fatalf("want lint, got %v", checkNames(res.Checks))
	}
	if c.Summary != "1 issue" || len(c.Issues) != 1 || c.Issues[0].Rule != "errcheck" || c.Issues[0].File != "a/a.go" {
		t.Errorf("lint check = %+v", c)
	}

	// --all keeps issues in every file.
	f = fakeAll().
		OnFunc("go test", profileWriter(aProfileFull)).
		On("golangci-lint", runner.Result{ExitCode: 1, Stdout: lintJSON})
	res = New().Gate(context.Background(), newEnv(t, repo, f), module.GateInput{All: true})
	c, _ = findCheck(res.Checks, "lint")
	if len(c.Issues) != 2 || c.Summary != "2 issues" {
		t.Errorf("all-mode lint = %+v", c)
	}
	if !f.Ran("go test -count=1") {
		t.Error("go test not run")
	}
	for _, call := range f.Calls() {
		if strings.HasPrefix(call.String(), "go test") && !slices.Contains(call.Args, "./...") {
			t.Errorf("all mode should test ./..., got %v", call.Args)
		}
	}
}

func TestGateLintMissingAndBroken(t *testing.T) {
	t.Parallel()
	repo := writeRepo(t, map[string]string{"a/a.go": aSource})
	file := filepath.Join(repo, "a/a.go")

	f := runner.NewFake("go").OnFunc("go test", profileWriter(aProfileFull))
	res := New().Gate(context.Background(), newEnv(t, repo, f), module.GateInput{Files: []string{file}})
	if c, ok := findCheck(res.Checks, "lint"); !ok || !strings.Contains(c.Summary, "not installed") {
		t.Errorf("missing lint: %+v", res.Checks)
	}

	f = fakeAll().OnFunc("go test", profileWriter(aProfileFull)).
		On("golangci-lint", runner.Result{ExitCode: 3, Stderr: "level=error msg=\"bad config\""})
	res = New().Gate(context.Background(), newEnv(t, repo, f), module.GateInput{Files: []string{file}})
	c, ok := findCheck(res.Checks, "lint")
	if !ok || c.Summary != "golangci-lint failed to run" || len(c.Lines) == 0 {
		t.Errorf("broken lint: %+v", res.Checks)
	}

	f = fakeAll().OnFunc("go test", profileWriter(aProfileFull)).
		On("golangci-lint", runner.Result{ExitCode: 1, Stdout: "{not json"})
	res = New().Gate(context.Background(), newEnv(t, repo, f), module.GateInput{Files: []string{file}})
	if c, ok := findCheck(res.Checks, "lint"); !ok || c.Summary != "golangci-lint failed to run" {
		t.Errorf("malformed json: %+v", res.Checks)
	}

	f = fakeAll().OnFunc("go test", profileWriter(aProfileFull)).OnError("golangci-lint", errors.New("exec failed"))
	res = New().Gate(context.Background(), newEnv(t, repo, f), module.GateInput{Files: []string{file}})
	if c, ok := findCheck(res.Checks, "lint"); !ok || !strings.Contains(c.Summary, "exec failed") {
		t.Errorf("lint start error: %+v", res.Checks)
	}
}

func TestGateModTidy(t *testing.T) {
	t.Parallel()
	repo := writeRepo(t, map[string]string{"a/a.go": aSource})
	file := filepath.Join(repo, "a/a.go")

	f := fakeAll().OnFunc("go test", profileWriter(aProfileFull)).
		On("go mod tidy", runner.Result{ExitCode: 1, Stdout: "--- go.mod\n+++ go.mod\n+require x v1\n"})
	res := New().Gate(context.Background(), newEnv(t, repo, f), module.GateInput{Files: []string{file}})
	if c, ok := findCheck(res.Checks, "mod-tidy"); !ok || c.Fix != "run `go mod tidy`" || len(c.Lines) != 3 {
		t.Errorf("tidy diff: %+v", res.Checks)
	}

	f = fakeAll().OnFunc("go test", profileWriter(aProfileFull)).
		On("go mod tidy", runner.Result{ExitCode: 1, Stderr: "dial tcp: lookup proxy.golang.org: no such host"})
	res = New().Gate(context.Background(), newEnv(t, repo, f), module.GateInput{Files: []string{file}})
	if _, ok := findCheck(res.Checks, "mod-tidy"); ok {
		t.Error("network failure must not block")
	}
	if len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "no such host") {
		t.Errorf("notes = %v", res.Notes)
	}

	f = fakeAll().OnFunc("go test", profileWriter(aProfileFull)).OnError("go mod tidy", errors.New("nope"))
	res = New().Gate(context.Background(), newEnv(t, repo, f), module.GateInput{Files: []string{file}})
	if len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "could not run") {
		t.Errorf("notes = %v", res.Notes)
	}
}

func TestGateVuln(t *testing.T) {
	t.Parallel()
	repo := writeRepo(t, map[string]string{"a/a.go": aSource})
	file := filepath.Join(repo, "a/a.go")
	vulnOut := "Vulnerability #1: GO-2024-0001\n    Bad thing\n  Found in: x@v1.0.0\n  Fixed in: x@v1.0.1\n"

	f := fakeAll().OnFunc("go test", profileWriter(aProfileFull)).On("govulncheck", runner.Result{ExitCode: 3, Stdout: vulnOut})
	res := New().Gate(context.Background(), newEnv(t, repo, f), module.GateInput{Files: []string{file}})
	c, ok := findCheck(res.Checks, "vuln")
	want := []string{"Vulnerability #1: GO-2024-0001", "Found in: x@v1.0.0", "Fixed in: x@v1.0.1"}
	if !ok || !slices.Equal(c.Lines, want) {
		t.Errorf("vuln = %+v", c)
	}

	f = fakeAll().OnFunc("go test", profileWriter(aProfileFull)).On("govulncheck", runner.Result{ExitCode: 3, Stdout: "something\nelse\n"})
	res = New().Gate(context.Background(), newEnv(t, repo, f), module.GateInput{Files: []string{file}})
	if c, _ := findCheck(res.Checks, "vuln"); !slices.Equal(c.Lines, []string{"something", "else"}) {
		t.Errorf("fallback lines = %v", c.Lines)
	}

	f = fakeAll().OnFunc("go test", profileWriter(aProfileFull)).On("govulncheck", runner.Result{ExitCode: 1, Stderr: "fetch db failed"})
	res = New().Gate(context.Background(), newEnv(t, repo, f), module.GateInput{Files: []string{file}})
	if len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "govulncheck failed") {
		t.Errorf("notes = %v", res.Notes)
	}

	f = fakeAll().OnFunc("go test", profileWriter(aProfileFull)).OnError("govulncheck", errors.New("x"))
	res = New().Gate(context.Background(), newEnv(t, repo, f), module.GateInput{Files: []string{file}})
	if len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "could not run") {
		t.Errorf("notes = %v", res.Notes)
	}

	f = runner.NewFake("go", "golangci-lint").OnFunc("go test", profileWriter(aProfileFull))
	res = New().Gate(context.Background(), newEnv(t, repo, f), module.GateInput{Files: []string{file}})
	if len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "govulncheck not installed") {
		t.Errorf("notes = %v", res.Notes)
	}

	f = runner.NewFake("go", "golangci-lint").OnFunc("go test", profileWriter(aProfileFull))
	env := newEnv(t, repo, f)
	env.Config.Go.Vuln = false
	res = New().Gate(context.Background(), env, module.GateInput{Files: []string{file}})
	if len(res.Notes) != 0 || slices.Contains(res.Ran, "vuln") {
		t.Errorf("vuln disabled: notes=%v ran=%v", res.Notes, res.Ran)
	}
}

func TestGateMultipleModulesAndOutsideFiles(t *testing.T) {
	t.Parallel()
	repo := writeRepo(t, map[string]string{
		"a/a.go":     aSource,
		"sub/go.mod": "module example.com/sub\n",
		"sub/s/s.go": "package s\n\nfunc S() int {\n\treturn 1\n}\n",
	})
	outside := filepath.Join(t.TempDir(), "x.go")
	if err := os.WriteFile(outside, []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := fakeAll().OnFunc("go test", func(c runner.Call) (runner.Result, error) {
		if strings.HasSuffix(c.Dir, "sub") {
			return profileWriter("mode: atomic\nexample.com/sub/s/s.go:3.14,5.2 1 1\n")(c)
		}
		return profileWriter(aProfileLow)(c)
	})
	res := New().Gate(context.Background(), newEnv(t, repo, f), module.GateInput{
		Files: []string{filepath.Join(repo, "a/a.go"), filepath.Join(repo, "sub/s/s.go"), outside},
	})
	if res.Total != 40 {
		t.Errorf("Total = %v, want 40 (2 of 5 statements)", res.Total)
	}
	if res.Coverage["example.com/sub/s"] != 100 || res.Coverage[modPath+"/a"] != 25 {
		t.Errorf("Coverage = %v", res.Coverage)
	}
	if strings.Count(strings.Join(res.Ran, ","), "build") != 1 {
		t.Errorf("Ran should be de-duplicated: %v", res.Ran)
	}
}

func TestFix(t *testing.T) {
	t.Parallel()
	gen := "// Code generated by x. DO NOT EDIT.\n\npackage a\n"
	repo := writeRepo(t, map[string]string{"a/a.go": aSource, "a/gen.go": gen})
	a := filepath.Join(repo, "a/a.go")
	files := []string{a, filepath.Join(repo, "a/gen.go"), filepath.Join(repo, "a/missing.go"), filepath.Join(repo, "go.mod")}

	f := fakeAll()
	res := New().Fix(context.Background(), newEnv(t, repo, f), files)
	if len(res.Notes) != 0 {
		t.Errorf("notes = %v", res.Notes)
	}
	calls := f.Calls()
	if len(calls) != 2 || calls[0].String() != "goimports -w "+a || calls[1].String() != "gofumpt -w "+a {
		t.Errorf("calls = %v", calls)
	}

	f = runner.NewFake("go")
	res = New().Fix(context.Background(), newEnv(t, repo, f), files)
	if len(res.Notes) != 2 {
		t.Errorf("want 2 notes, got %v", res.Notes)
	}
	if !f.Ran("gofmt -w "+a) || f.Ran("goimports") || f.Ran("gofumpt") {
		t.Errorf("calls = %v", f.Calls())
	}

	f = fakeAll()
	res = New().Fix(context.Background(), newEnv(t, repo, f), []string{filepath.Join(repo, "a/gen.go")})
	if len(f.Calls()) != 0 || len(res.Notes) != 0 {
		t.Errorf("generated files must not be formatted: %v", f.Calls())
	}
}

func TestCheck(t *testing.T) {
	t.Parallel()
	repo := writeRepo(t, map[string]string{"a/a.go": aSource, "a/other.go": "package a\n", "b/b.go": bSource})
	a := filepath.Join(repo, "a/a.go")

	t.Run("vet issues filtered to touched files", func(t *testing.T) {
		t.Parallel()
		f := fakeAll().On("go vet", runner.Result{ExitCode: 1, Stderr: "# example.com/demo/a\na/a.go:4:2: printf mismatch\na/other.go:1:1: unrelated\n"})
		checks := New().Check(context.Background(), newEnv(t, repo, f), []string{a})
		c, ok := findCheck(checks, "vet")
		if !ok || len(c.Issues) != 1 || c.Issues[0].File != "a/a.go" || c.Summary != "1 issue" {
			t.Fatalf("vet = %+v", checks)
		}
		if !f.Ran("go vet ./a") {
			t.Errorf("calls = %v", f.Calls())
		}
	})

	t.Run("type errors keep every file", func(t *testing.T) {
		t.Parallel()
		f := fakeAll().On("go vet", runner.Result{ExitCode: 1, Stderr: "vet: a/other.go:1:1: undefined: Foo\na/a.go:4:2: undefined: Bar\n"})
		checks := New().Check(context.Background(), newEnv(t, repo, f), []string{a})
		c, _ := findCheck(checks, "vet")
		if len(c.Issues) != 2 || c.Summary != "2 issues" {
			t.Fatalf("vet = %+v", c)
		}
	})

	t.Run("vet issues only elsewhere", func(t *testing.T) {
		t.Parallel()
		f := fakeAll().On("go vet", runner.Result{ExitCode: 1, Stderr: "a/other.go:1:1: unrelated\n"})
		checks := New().Check(context.Background(), newEnv(t, repo, f), []string{a})
		if _, ok := findCheck(checks, "vet"); ok {
			t.Fatalf("got %+v", checks)
		}
	})

	t.Run("vet start error", func(t *testing.T) {
		t.Parallel()
		f := fakeAll().OnError("go vet", errors.New("gone"))
		checks := New().Check(context.Background(), newEnv(t, repo, f), []string{a})
		if c, ok := findCheck(checks, "vet"); !ok || !strings.Contains(c.Summary, "gone") {
			t.Fatalf("got %+v", checks)
		}
	})

	t.Run("lint modes", func(t *testing.T) {
		t.Parallel()
		for mode, wantFast := range map[string]bool{"fast": true, "full": false} {
			f := fakeAll().On("golangci-lint", runner.Result{ExitCode: 1, Stdout: lintJSON})
			env := newEnv(t, repo, f)
			env.Config.Go.Lint.OnEdit = mode
			checks := New().Check(context.Background(), env, []string{a})
			c, ok := findCheck(checks, "lint")
			if !ok || len(c.Issues) != 1 {
				t.Fatalf("%s: lint = %+v", mode, checks)
			}
			if got := f.Ran("golangci-lint run --output.json.path=stdout"); !got {
				t.Errorf("%s: calls = %v", mode, f.Calls())
			}
			fast := false
			for _, call := range f.Calls() {
				if call.Name == "golangci-lint" && slices.Contains(call.Args, "--fast-only") {
					fast = true
				}
			}
			if fast != wantFast {
				t.Errorf("%s: --fast-only = %v", mode, fast)
			}
		}

		f := fakeAll()
		env := newEnv(t, repo, f)
		env.Config.Go.Lint.OnEdit = "off"
		if checks := New().Check(context.Background(), env, []string{a}); len(checks) != 0 || f.Ran("golangci-lint") {
			t.Errorf("lint off: checks=%v calls=%v", checks, f.Calls())
		}
	})

	t.Run("no packages", func(t *testing.T) {
		t.Parallel()
		f := fakeAll()
		checks := New().Check(context.Background(), newEnv(t, repo, f), []string{filepath.Join(repo, "go.mod"), "/nowhere/x.go"})
		if len(checks) != 0 || len(f.Calls()) != 0 {
			t.Errorf("checks=%v calls=%v", checks, f.Calls())
		}
	})
}

func TestLintConfig(t *testing.T) {
	t.Parallel()
	m := New()

	t.Run("configured file in repo", func(t *testing.T) {
		t.Parallel()
		repo := writeRepo(t, map[string]string{".golangci.yml": "version: \"2\"\n"})
		env := newEnv(t, repo, fakeAll())
		if got := m.lintConfig(env, repo); got != filepath.Join(repo, ".golangci.yml") {
			t.Errorf("got %q", got)
		}
	})

	t.Run("absolute configured path", func(t *testing.T) {
		t.Parallel()
		repo := writeRepo(t, nil)
		abs := filepath.Join(t.TempDir(), "lint.yml")
		if err := os.WriteFile(abs, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		env := newEnv(t, repo, fakeAll())
		env.Config.Go.Lint.Config = abs
		if got := m.lintConfig(env, repo); got != abs {
			t.Errorf("got %q", got)
		}
	})

	t.Run("module root has its own config", func(t *testing.T) {
		t.Parallel()
		repo := writeRepo(t, map[string]string{".golangci.yaml": "x"})
		env := newEnv(t, repo, fakeAll())
		if got := m.lintConfig(env, repo); got != "" {
			t.Errorf("got %q, want empty so golangci-lint finds it", got)
		}
	})

	t.Run("bundled config written to data dir", func(t *testing.T) {
		t.Parallel()
		repo := writeRepo(t, nil)
		env := newEnv(t, repo, fakeAll())
		got := m.lintConfig(env, repo)
		if got != filepath.Join(env.DataDir, "golangci.yml") {
			t.Fatalf("got %q", got)
		}
		data, err := os.ReadFile(got)
		if err != nil || string(data) != string(BundledLintConfig) {
			t.Errorf("bundled config not written: %v", err)
		}
	})

	t.Run("no data dir", func(t *testing.T) {
		t.Parallel()
		repo := writeRepo(t, nil)
		env := newEnv(t, repo, fakeAll())
		env.DataDir = ""
		if got := m.lintConfig(env, repo); got != "" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("unwritable data dir", func(t *testing.T) {
		t.Parallel()
		repo := writeRepo(t, nil)
		blocker := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(blocker, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		env := newEnv(t, repo, fakeAll())
		env.DataDir = filepath.Join(blocker, "sub")
		if got := m.lintConfig(env, repo); got != "" {
			t.Errorf("got %q", got)
		}
	})
}

func TestModulePath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, err := ModulePath(dir); err == nil {
		t.Error("want error for missing go.mod")
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("// c\nmodule \"example.com/q\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := ModulePath(dir); err != nil || got != "example.com/q" {
		t.Errorf("ModulePath = %q, %v", got, err)
	}
	if got := FindModuleRoot(filepath.Join(dir, "x", "y")); got != dir {
		t.Errorf("FindModuleRoot = %q", got)
	}
	if got := FindModuleRoot(string(filepath.Separator)); got != "" && got != string(filepath.Separator) {
		t.Errorf("FindModuleRoot(/) = %q", got)
	}
}

func TestHelpers(t *testing.T) {
	t.Parallel()
	if got := ranges([]int{1, 2, 3, 5, 7, 8}); got != "1-3,5,7-8" {
		t.Errorf("ranges = %q", got)
	}
	if got := ranges(nil); got != "" {
		t.Errorf("ranges(nil) = %q", got)
	}
	if got := capList([]string{"a", "b"}, 5); len(got) != 2 {
		t.Errorf("capList = %v", got)
	}
	if got := lastLines("a\nb\n\nc\n", 2); !slices.Equal(got, []string{"b", "c"}) {
		t.Errorf("lastLines = %v", got)
	}
	if got := firstLines("a\n\nb\nc", 2); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("firstLines = %v", got)
	}
	if got := displayPath("/repo", "/elsewhere/x.go"); got != "/elsewhere/x.go" {
		t.Errorf("displayPath = %q", got)
	}
	if plural(1) != "" || plural(2) != "s" {
		t.Error("plural")
	}
	if got := appendUnique([]string{"a"}, "a", "b", "b"); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("appendUnique = %v", got)
	}
	if got := funcAt(nil, "/x.go", 3); got != "(top level)" {
		t.Errorf("funcAt = %q", got)
	}
	if allLines("/does/not/exist") != nil {
		t.Error("allLines of missing file should be nil")
	}
	if excluded("/repo", "/repo/x.go", nil) {
		t.Error("missing file is not excluded")
	}
	if _, err := parseLint("no json here"); err == nil {
		t.Error("parseLint should fail without JSON")
	}
}

func TestAllGoFiles(t *testing.T) {
	t.Parallel()
	repo := writeRepo(t, map[string]string{
		"a/a.go":              aSource,
		"vendor/v/v.go":       "package v\n",
		"a/testdata/t.go":     "package t\n",
		".hidden/h.go":        "package h\n",
		"node_modules/n/n.go": "package n\n",
		"README.md":           "x",
	})
	got := allGoFiles(repo)
	if !slices.Equal(got, []string{filepath.Join(repo, "a/a.go")}) {
		t.Errorf("allGoFiles = %v", got)
	}
}

// noLintRunner runs real commands but hides golangci-lint and govulncheck.
type noLintRunner struct{ runner.Exec }

func (r noLintRunner) LookPath(name string) (string, error) {
	if name == "golangci-lint" || name == "govulncheck" {
		return "", runner.ErrNotFound
	}
	return r.Exec.LookPath(name)
}

func TestGateIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test runs the real go toolchain")
	}
	if _, err := (runner.Exec{}).LookPath("go"); err != nil {
		t.Skip("go toolchain not installed")
	}
	t.Parallel()
	repo := writeRepo(t, map[string]string{
		"a/a.go":      aSource,
		"a/a_test.go": "package a\n\nimport \"testing\"\n\nfunc TestCovered(t *testing.T) {\n\tif Covered() != 1 {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n",
	})
	env := module.Env{Runner: noLintRunner{}, Config: config.Default(), Repo: repo, DataDir: t.TempDir()}
	env.Config.Go.Race = false
	env.Config.Go.Vuln = false
	res := New().Gate(context.Background(), env, module.GateInput{Files: []string{filepath.Join(repo, "a/a.go")}})

	c, ok := findCheck(res.Checks, "coverage")
	if !ok {
		t.Fatalf("want coverage check, got %+v", res.Checks)
	}
	if !slices.Contains(c.Lines, "a/a.go:7  Uncovered  0/3 stmts") {
		t.Errorf("lines = %q", c.Lines)
	}
	if res.Total != 25 {
		t.Errorf("Total = %v, want 25", res.Total)
	}
	if _, ok := findCheck(res.Checks, "lint"); !ok {
		t.Error("missing golangci-lint must fail the lint check")
	}
	if _, ok := findCheck(res.Checks, "build"); ok {
		t.Errorf("build should pass: %+v", res.Checks)
	}
}
