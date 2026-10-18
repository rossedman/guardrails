package python

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rossedman/guardrails/internal/config"
	"github.com/rossedman/guardrails/internal/feedback"
	"github.com/rossedman/guardrails/internal/module"
	"github.com/rossedman/guardrails/internal/runner"
	"github.com/rossedman/guardrails/internal/tools"
)

// repo builds a temp repo from relative path → content.
func repo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if strings.Contains(rel, ".venv/bin/") {
			mode = 0o755
		}
		if err := os.WriteFile(p, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func env(dir string, f *runner.Fake) module.Env {
	return module.Env{Runner: f, Config: config.Default(), Repo: dir, DataDir: filepath.Join(dir, ".data")}
}

// resolverFunc adapts a function to tools.Resolver.
type resolverFunc func(name string) (tools.Command, error)

func (r resolverFunc) Resolve(_ context.Context, name string) (tools.Command, error) { return r(name) }

func ruffJSON(issues ...string) string { return "[" + strings.Join(issues, ",") + "]" }

func ruffIssueJSON(file string, line int, code, msg string) string {
	c := "null"
	if code != "" {
		c = fmt.Sprintf("%q", code)
	}
	return fmt.Sprintf(`{"code":%s,"message":%q,"filename":%q,"location":{"row":%d,"column":1}}`, c, msg, file, line)
}

// covJSON renders a coverage.py report.
func covJSON(files map[string][2][]int) string {
	type f struct {
		Executed []int `json:"executed_lines"`
		Missing  []int `json:"missing_lines"`
		Summary  struct {
			Covered int `json:"covered_lines"`
			Num     int `json:"num_statements"`
		} `json:"summary"`
	}
	out := map[string]map[string]f{"files": {}}
	for name, lines := range files {
		var v f
		v.Executed, v.Missing = lines[0], lines[1]
		if v.Executed == nil {
			v.Executed = []int{}
		}
		if v.Missing == nil {
			v.Missing = []int{}
		}
		v.Summary.Covered = len(lines[0])
		v.Summary.Num = len(lines[0]) + len(lines[1])
		out["files"][name] = v
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// writeCov makes a fake pytest run write report to the --cov-report path.
func writeCov(report string, res runner.Result) func(runner.Call) (runner.Result, error) {
	return func(c runner.Call) (runner.Result, error) {
		for _, a := range c.Args {
			if p, ok := strings.CutPrefix(a, "--cov-report=json:"); ok {
				if report != "" {
					if err := os.WriteFile(p, []byte(report), 0o600); err != nil {
						return runner.Result{}, err
					}
				}
			}
		}
		return res, nil
	}
}

func checkNames(cs []feedback.Check) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Name)
	}
	return out
}

func find(cs []feedback.Check, name string) (feedback.Check, bool) {
	for _, c := range cs {
		if c.Name == name {
			return c, true
		}
	}
	return feedback.Check{}, false
}

func TestBasics(t *testing.T) {
	t.Parallel()
	m := New()
	if m.Name() != "python" {
		t.Fatal(m.Name())
	}
	cfg := config.Default()
	if !m.Enabled(cfg) {
		t.Fatal("enabled by default")
	}
	off := false
	cfg.Python.Enabled = &off
	if m.Enabled(cfg) {
		t.Fatal("disabled")
	}
	for path, want := range map[string]bool{"a/b.py": true, "pyproject.toml": true, "x/pyproject.toml": true, "a.pyc": false, "setup.cfg": false, "main.go": false} {
		if got := m.Match(path); got != want {
			t.Errorf("Match(%q) = %v", path, got)
		}
	}
	if len(m.Tools()) != 3 || m.tool("mypy").Name != "mypy" || m.tool("nope").Install != "" {
		t.Fatal("tools")
	}
}

func TestStandards(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	s := New().Standards(cfg)
	for _, want := range []string{"ruff format", "mypy --strict", "90% coverage", "starting coverage", "changed this session", "uv.lock"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in %s", want, s)
		}
	}
	cfg.Python.Typecheck, cfg.Python.PatchCoverage, cfg.Python.Coverage.Ratchet = "pyright", 0, false
	s = New().Standards(cfg)
	if !strings.Contains(s, "pyright") || strings.Contains(s, "changed this session") || strings.Contains(s, "starting coverage") {
		t.Error(s)
	}
	cfg.Python.Typecheck, cfg.Python.Strict = "mypy", false
	if s = New().Standards(cfg); strings.Contains(s, "--strict") {
		t.Error(s)
	}
	cfg.Python.Typecheck = "off"
	if s = New().Standards(cfg); strings.Contains(s, "mypy") || strings.Contains(s, "pyright") {
		t.Error(s)
	}
}

func TestFix(t *testing.T) {
	t.Parallel()
	t.Run("runs format then check", func(t *testing.T) {
		t.Parallel()
		dir := repo(t, map[string]string{"a.py": "x=1\n", "pyproject.toml": ""})
		f := runner.NewFake("ruff")
		res := New().Fix(context.Background(), env(dir, f), []string{filepath.Join(dir, "a.py"), filepath.Join(dir, "pyproject.toml"), filepath.Join(dir, "gone.py")})
		calls := f.Calls()
		if len(res.Notes) != 0 || len(calls) != 2 || calls[0].Args[0] != "format" || calls[1].Args[0] != "check" || calls[1].Args[1] != "--fix" {
			t.Fatalf("calls %v notes %v", calls, res.Notes)
		}
	})
	t.Run("prefers project venv", func(t *testing.T) {
		t.Parallel()
		dir := repo(t, map[string]string{"a.py": "", ".venv/bin/ruff": "#!/bin/sh\n"})
		f := runner.NewFake()
		New().Fix(context.Background(), env(dir, f), []string{filepath.Join(dir, "a.py")})
		if !f.Ran(filepath.Join(dir, ".venv/bin/ruff") + " format") {
			t.Fatalf("calls %v", f.Calls())
		}
	})
	t.Run("ruff unavailable", func(t *testing.T) {
		t.Parallel()
		dir := repo(t, map[string]string{"a.py": ""})
		res := New().Fix(context.Background(), env(dir, runner.NewFake()), []string{filepath.Join(dir, "a.py")})
		if len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "ruff is not installed") {
			t.Fatal(res.Notes)
		}
	})
	t.Run("resolver error is silent", func(t *testing.T) {
		t.Parallel()
		dir := repo(t, map[string]string{"a.py": ""})
		e := env(dir, runner.NewFake())
		e.Tools = resolverFunc(func(string) (tools.Command, error) { return tools.Command{}, errors.New("boom") })
		if res := New().Fix(context.Background(), e, []string{filepath.Join(dir, "a.py")}); len(res.Notes) != 0 {
			t.Fatal(res.Notes)
		}
	})
}

func TestCheck(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	newRepo := func(t *testing.T) (string, string) {
		dir := repo(t, map[string]string{"pkg/a.py": "", "pkg/b.py": "", "pyproject.toml": ""})
		return dir, filepath.Join(dir, "pkg/a.py")
	}
	t.Run("ruff and mypy issues in the edited file", func(t *testing.T) {
		t.Parallel()
		dir, a := newRepo(t)
		f := runner.NewFake("ruff", "mypy").
			On("ruff check", runner.Result{ExitCode: 1, Stdout: ruffJSON(
				ruffIssueJSON(a, 3, "F401", "unused import"),
				ruffIssueJSON("pkg/b.py", 1, "E501", "too long"),
				ruffIssueJSON(a, 9, "", "SyntaxError"),
			)}).
			On("mypy", runner.Result{ExitCode: 1, Stdout: "pkg/a.py:4:5: error: Incompatible types  [assignment]\npkg/a.py:4:5: note: see docs\npkg/b.py:1: error: Other\npkg/a.py:7: error: No code here\n"})
		checks := New().Check(ctx, env(dir, f), []string{a})
		if got := checkNames(checks); strings.Join(got, ",") != "mypy,ruff" {
			t.Fatalf("checks %v", got)
		}
		ruff, _ := find(checks, "ruff")
		if len(ruff.Issues) != 2 || ruff.Issues[0].File != "pkg/a.py" || ruff.Issues[0].Rule != "F401" || ruff.Issues[1].Rule != "syntax" {
			t.Errorf("ruff %+v", ruff.Issues)
		}
		my, _ := find(checks, "mypy")
		if len(my.Issues) != 2 || my.Issues[0].Rule != "assignment" || my.Issues[0].Col != 5 || my.Issues[1].Rule != "mypy" {
			t.Errorf("mypy %+v", my.Issues)
		}
		var mypyCall runner.Call
		for _, c := range f.Calls() {
			if c.Name == "mypy" {
				mypyCall = c
			}
		}
		line := mypyCall.String()
		if !strings.Contains(line, "--strict") || !strings.Contains(line, "--cache-dir") || mypyCall.Dir != dir {
			t.Errorf("mypy call %s in %s", line, mypyCall.Dir)
		}
	})
	t.Run("clean", func(t *testing.T) {
		t.Parallel()
		dir, a := newRepo(t)
		f := runner.NewFake("ruff", "mypy").On("ruff check", runner.Result{Stdout: "[]"})
		if checks := New().Check(ctx, env(dir, f), []string{a}); len(checks) != 0 {
			t.Fatal(checks)
		}
	})
	t.Run("venv python and no strict", func(t *testing.T) {
		t.Parallel()
		dir := repo(t, map[string]string{"a.py": "", ".venv/bin/python": "", ".venv/bin/mypy": ""})
		f := runner.NewFake("ruff").On("ruff check", runner.Result{Stdout: "[]"})
		e := env(dir, f)
		e.Config.Python.Strict = false
		e.DataDir = ""
		New().Check(ctx, e, []string{filepath.Join(dir, "a.py")})
		var line string
		for _, c := range f.Calls() {
			if strings.HasSuffix(c.Name, "mypy") {
				line = c.String()
			}
		}
		if !strings.Contains(line, "--python-executable "+filepath.Join(dir, ".venv/bin/python")) || strings.Contains(line, "--strict") || strings.Contains(line, "--cache-dir") {
			t.Fatal(line)
		}
	})
	t.Run("tool failures", func(t *testing.T) {
		t.Parallel()
		dir, a := newRepo(t)
		f := runner.NewFake("ruff", "mypy").
			On("ruff check", runner.Result{ExitCode: 2, Stderr: "ruff crashed"}).
			On("mypy", runner.Result{ExitCode: 2, Stderr: "mypy: error: bad flag"})
		checks := New().Check(ctx, env(dir, f), []string{a})
		r, _ := find(checks, "ruff")
		my, _ := find(checks, "mypy")
		if r.Summary != "ruff failed to run" || my.Summary != "mypy failed to run" {
			t.Fatalf("%+v", checks)
		}
	})
	t.Run("ruff non-json exit 0 and start errors", func(t *testing.T) {
		t.Parallel()
		dir, a := newRepo(t)
		f := runner.NewFake("ruff", "mypy").
			On("ruff check", runner.Result{Stdout: "All checks passed!"}).
			OnError("mypy", errors.New("exec failed"))
		checks := New().Check(ctx, env(dir, f), []string{a})
		if len(checks) != 1 || checks[0].Name != "mypy" || !strings.Contains(checks[0].Summary, "exec failed") {
			t.Fatalf("%+v", checks)
		}
		f2 := runner.NewFake("ruff").OnError("ruff", errors.New("nope"))
		e := env(dir, f2)
		e.Config.Python.Typecheck = "off"
		checks = New().Check(ctx, e, []string{a})
		if len(checks) != 1 || !strings.Contains(checks[0].Summary, "nope") {
			t.Fatalf("%+v", checks)
		}
	})
	t.Run("unavailable tools are skipped", func(t *testing.T) {
		t.Parallel()
		dir, a := newRepo(t)
		if checks := New().Check(ctx, env(dir, runner.NewFake()), []string{a}); len(checks) != 0 {
			t.Fatal(checks)
		}
	})
	t.Run("resolver errors surface", func(t *testing.T) {
		t.Parallel()
		dir, a := newRepo(t)
		e := env(dir, runner.NewFake())
		e.Tools = resolverFunc(func(string) (tools.Command, error) { return tools.Command{}, errors.New("broken lock") })
		checks := New().Check(ctx, e, []string{a})
		if len(checks) != 2 {
			t.Fatalf("%+v", checks)
		}
	})
	t.Run("pyright", func(t *testing.T) {
		t.Parallel()
		dir, a := newRepo(t)
		report := `{"generalDiagnostics":[
			{"file":"` + a + `","severity":"error","message":"Bad type\nmore","rule":"reportGeneralTypeIssues","range":{"start":{"line":2,"character":4}}},
			{"file":"` + a + `","severity":"warning","message":"meh","range":{"start":{"line":0,"character":0}}},
			{"file":"` + a + `","severity":"error","message":"No rule","range":{"start":{"line":0,"character":0}}}]}`
		f := runner.NewFake("ruff", "pyright").On("ruff check", runner.Result{Stdout: "[]"}).On("pyright", runner.Result{ExitCode: 1, Stdout: report})
		e := env(dir, f)
		e.Config.Python.Typecheck = "pyright"
		checks := New().Check(ctx, e, []string{a})
		if len(checks) != 1 || len(checks[0].Issues) != 2 || checks[0].Issues[0].Line != 3 || checks[0].Issues[0].Col != 5 || checks[0].Issues[0].Message != "Bad type" || checks[0].Issues[1].Rule != "pyright" {
			t.Fatalf("%+v", checks)
		}
		// Unparseable output.
		f = runner.NewFake("ruff", "pyright").On("ruff check", runner.Result{Stdout: "[]"}).On("pyright", runner.Result{ExitCode: 3, Stdout: "boom"})
		e.Runner = f
		checks = New().Check(ctx, e, []string{a})
		if len(checks) != 1 || checks[0].Summary != "pyright failed to run" {
			t.Fatalf("%+v", checks)
		}
		f = runner.NewFake("ruff", "pyright").On("ruff check", runner.Result{Stdout: "[]"}).On("pyright", runner.Result{Stdout: "ok"})
		e.Runner = f
		if checks = New().Check(ctx, e, []string{a}); len(checks) != 0 {
			t.Fatalf("%+v", checks)
		}
		f = runner.NewFake("ruff", "pyright").On("ruff check", runner.Result{Stdout: "[]"}).OnError("pyright", errors.New("x"))
		e.Runner = f
		if checks = New().Check(ctx, e, []string{a}); len(checks) != 1 {
			t.Fatalf("%+v", checks)
		}
		// Missing pyright is skipped on edit.
		e.Runner = runner.NewFake("ruff").On("ruff check", runner.Result{Stdout: "[]"})
		if checks = New().Check(ctx, e, []string{a}); len(checks) != 0 {
			t.Fatalf("%+v", checks)
		}
	})
}

type gateFixture struct {
	dir string
	a   string
	f   *runner.Fake
	e   module.Env
}

func newGate(t *testing.T, files map[string]string) gateFixture {
	t.Helper()
	base := map[string]string{"pyproject.toml": "[project]\nname = \"x\"\n", "pkg/a.py": "x = 1\n", "pkg/test_a.py": ""}
	for k, v := range files {
		base[k] = v
	}
	dir := repo(t, base)
	f := runner.NewFake("ruff", "mypy", "uv").On("ruff check", runner.Result{Stdout: "[]"})
	return gateFixture{dir: dir, a: filepath.Join(dir, "pkg/a.py"), f: f, e: env(dir, f)}
}

func TestGatePasses(t *testing.T) {
	t.Parallel()
	g := newGate(t, nil)
	g.f.OnFunc("env", writeCov(covJSON(map[string][2][]int{"pkg/a.py": {{1, 2, 3}, nil}}), runner.Result{}))
	res := New().Gate(context.Background(), g.e, module.GateInput{Files: []string{g.a, filepath.Join(g.dir, "pkg/test_a.py")}})
	if len(res.Checks) != 0 {
		t.Fatalf("%+v", res.Checks)
	}
	if res.Total != 100 || res.Coverage["python:pkg"] != 100 {
		t.Fatalf("total %v cov %v", res.Total, res.Coverage)
	}
	if strings.Join(res.Ran, ",") != "ruff,tests,coverage,mypy" {
		t.Fatal(res.Ran)
	}
	var uvLine string
	for _, c := range g.f.Calls() {
		if c.Name == "env" {
			uvLine = c.String()
		}
	}
	if !strings.Contains(uvLine, "run --no-project --with-editable "+g.dir) || !strings.Contains(uvLine, "--cov=.") {
		t.Fatal(uvLine)
	}
}

func TestTestCommand(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cases := []struct {
		name   string
		files  map[string]string
		fake   *runner.Fake
		want   string
		errStr string
	}{
		{"uv lock", map[string]string{"uv.lock": ""}, runner.NewFake("uv"), "uv run --frozen --with pytest", ""},
		{"venv with uv", map[string]string{".venv/bin/python": ""}, runner.NewFake("uv"), "uv run --no-project --python", ""},
		{"plain uv no project table", nil, runner.NewFake("uv"), "uv run --no-project --with pytest", ""},
		{"venv only", map[string]string{".venv/bin/python": ""}, runner.NewFake(), ".venv/bin/python -m pytest", ""},
		{"nothing", nil, runner.NewFake(), "", "uv is not installed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := repo(t, tc.files)
			cmd, args, err := New().testCommand(ctx, env(dir, tc.fake), dir)
			if tc.errStr != "" {
				if err == nil || !strings.Contains(module.Unavailable(module.Tool{Name: "uv"}, err), tc.errStr) {
					t.Fatalf("err %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			line := strings.Join(append([]string{cmd.Path}, args...), " ")
			if !strings.Contains(line, tc.want) {
				t.Fatalf("%s does not contain %s", line, tc.want)
			}
		})
	}
	t.Run("broken resolver", func(t *testing.T) {
		t.Parallel()
		dir := repo(t, nil)
		e := env(dir, runner.NewFake())
		e.Tools = resolverFunc(func(string) (tools.Command, error) { return tools.Command{}, errors.New("x") })
		if _, _, err := New().testCommand(ctx, e, dir); !errors.Is(err, errNoEnv) {
			t.Fatal(err)
		}
		res := New().Gate(ctx, e, module.GateInput{Files: nil, All: true})
		_ = res
	})
}

func TestGateFailures(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("failing tests", func(t *testing.T) {
		t.Parallel()
		g := newGate(t, nil)
		g.f.On("env", runner.Result{ExitCode: 1, Stdout: "FAILED pkg/test_a.py::test_x - AssertionError\nERROR pkg/test_a.py::test_y\nFAILED pkg/test_a.py::test_x - again\n"})
		res := New().Gate(ctx, g.e, module.GateInput{Files: []string{g.a}})
		c, ok := find(res.Checks, "tests")
		if !ok || c.Summary != "2 failing tests" || len(c.Lines) != 2 {
			t.Fatalf("%+v", res.Checks)
		}
	})
	t.Run("pytest error without ids", func(t *testing.T) {
		t.Parallel()
		g := newGate(t, nil)
		g.f.On("env", runner.Result{ExitCode: 4, Stderr: "usage error"})
		res := New().Gate(ctx, g.e, module.GateInput{Files: []string{g.a}})
		c, _ := find(res.Checks, "tests")
		if c.Summary != "pytest exited 4" || len(c.Lines) == 0 {
			t.Fatalf("%+v", c)
		}
	})
	t.Run("run error", func(t *testing.T) {
		t.Parallel()
		g := newGate(t, nil)
		g.f.OnError("env", errors.New("killed"))
		res := New().Gate(ctx, g.e, module.GateInput{Files: []string{g.a}})
		if c, _ := find(res.Checks, "tests"); !strings.Contains(c.Summary, "killed") {
			t.Fatalf("%+v", res.Checks)
		}
	})
	t.Run("no report", func(t *testing.T) {
		t.Parallel()
		g := newGate(t, nil)
		res := New().Gate(ctx, g.e, module.GateInput{Files: []string{g.a}})
		if c, _ := find(res.Checks, "coverage"); c.Summary != "pytest-cov wrote no report" {
			t.Fatalf("%+v", res.Checks)
		}
	})
	t.Run("corrupt report", func(t *testing.T) {
		t.Parallel()
		g := newGate(t, nil)
		g.f.OnFunc("env", writeCov("{not json", runner.Result{}))
		res := New().Gate(ctx, g.e, module.GateInput{Files: []string{g.a}})
		if c, _ := find(res.Checks, "coverage"); !strings.Contains(c.Summary, "parse coverage report") {
			t.Fatalf("%+v", res.Checks)
		}
	})
	t.Run("subproject coverage names the project", func(t *testing.T) {
		t.Parallel()
		g := newGate(t, map[string]string{"sub/pyproject.toml": "", "sub/m.py": ""})
		g.f.OnFunc("env", writeCov(covJSON(map[string][2][]int{"m.py": {nil, {1}}}), runner.Result{}))
		res := New().Gate(ctx, g.e, module.GateInput{Files: []string{filepath.Join(g.dir, "sub/m.py")}})
		if c, _ := find(res.Checks, "coverage"); c.Summary != "0.0% < 90.0% (sub)" {
			t.Fatalf("%+v", res.Checks)
		}
	})
	t.Run("coverage below threshold and ratchet", func(t *testing.T) {
		t.Parallel()
		g := newGate(t, map[string]string{"pkg/b.py": "", "lib/c.py": ""})
		g.f.OnFunc("env", writeCov(covJSON(map[string][2][]int{
			"pkg/a.py": {{1}, {2, 3, 4, 7}},
			"pkg/b.py": {{1, 2}, {5}},
			"lib/c.py": {{1}, nil},
			"lib/d.py": {nil, nil},
		}), runner.Result{ExitCode: 5}))
		res := New().Gate(ctx, g.e, module.GateInput{
			Files:    []string{g.a},
			Baseline: map[string]float64{"python:pkg": 80, "python:lib": 50},
		})
		c, ok := find(res.Checks, "coverage")
		if !ok || c.Summary != "44.4% < 90.0%" {
			t.Fatalf("%+v", res.Checks)
		}
		joined := strings.Join(c.Lines, "\n")
		for _, want := range []string{"pkg  37.5% dropped below its session baseline of 80.0%", "pkg/a.py  1/5 stmts  missing 2-4,7", "pkg/b.py  2/3 stmts  missing 5"} {
			if !strings.Contains(joined, want) {
				t.Errorf("missing %q in\n%s", want, joined)
			}
		}
		if strings.Contains(joined, "lib/c.py") {
			t.Error("fully covered file listed")
		}
	})
	t.Run("ratchet alone", func(t *testing.T) {
		t.Parallel()
		g := newGate(t, nil)
		g.f.OnFunc("env", writeCov(covJSON(map[string][2][]int{"pkg/a.py": {{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19}, {20}}}), runner.Result{}))
		res := New().Gate(ctx, g.e, module.GateInput{Files: []string{g.a}, Baseline: map[string]float64{"python:pkg": 100}})
		c, ok := find(res.Checks, "coverage")
		if !ok || !strings.HasPrefix(c.Summary, "95.0% total") {
			t.Fatalf("%+v", res.Checks)
		}
		g.e.Config.Python.Coverage.Ratchet = false
		res = New().Gate(ctx, g.e, module.GateInput{Files: []string{g.a}, Baseline: map[string]float64{"python:pkg": 100}})
		if _, ok := find(res.Checks, "coverage"); ok {
			t.Fatal("ratchet disabled")
		}
	})
	t.Run("patch coverage", func(t *testing.T) {
		t.Parallel()
		g := newGate(t, map[string]string{"pkg/new.py": "a\nb\nc\n"})
		g.f.OnFunc("env", writeCov(covJSON(map[string][2][]int{
			"pkg/a.py":   {{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, {11, 12}},
			"pkg/new.py": {{1}, {2, 3}},
		}), runner.Result{}))
		g.f.On("git diff", runner.Result{Stdout: "+++ b/pkg/a.py\n@@ -0,0 +10,3 @@\n+x\n+y\n+z\n"})
		g.f.On("git ls-files", runner.Result{Stdout: "pkg/new.py\n"})
		g.e.Config.Python.Coverage.Total = 50
		res := New().Gate(ctx, g.e, module.GateInput{Files: []string{g.a, filepath.Join(g.dir, "pkg/new.py"), filepath.Join(g.dir, "pkg/test_a.py")}, BaseCommit: "abc"})
		c, ok := find(res.Checks, "patch-coverage")
		if !ok || !strings.Contains(c.Summary, "(2/6)") {
			t.Fatalf("%+v", res.Checks)
		}
		if strings.Join(c.Lines, "|") != "pkg/a.py:11-12|pkg/new.py:2-3" {
			t.Fatal(c.Lines)
		}
		for _, call := range g.f.Calls() {
			if call.Name == "git" && strings.Contains(call.String(), "test_a.py") {
				t.Error("test files are not patch targets")
			}
		}
		if !strings.Contains(strings.Join(res.Ran, ","), "patch-coverage") {
			t.Error(res.Ran)
		}
	})
	t.Run("patch coverage met or skipped", func(t *testing.T) {
		t.Parallel()
		g := newGate(t, nil)
		g.f.OnFunc("env", writeCov(covJSON(map[string][2][]int{"pkg/a.py": {{1, 2}, nil}}), runner.Result{}))
		g.f.On("git diff", runner.Result{Stdout: "+++ b/pkg/a.py\n@@ -0,0 +1 @@\n+x\n"})
		res := New().Gate(ctx, g.e, module.GateInput{Files: []string{g.a}, BaseCommit: "abc"})
		if len(res.Checks) != 0 {
			t.Fatalf("%+v", res.Checks)
		}
		// Only test files touched: nothing to diff.
		res = New().Gate(ctx, g.e, module.GateInput{Files: []string{filepath.Join(g.dir, "pkg/test_a.py")}, BaseCommit: "abc"})
		if len(res.Checks) != 0 {
			t.Fatalf("%+v", res.Checks)
		}
	})
	t.Run("missing tools", func(t *testing.T) {
		t.Parallel()
		dir := repo(t, map[string]string{"a.py": ""})
		res := New().Gate(ctx, env(dir, runner.NewFake()), module.GateInput{Files: []string{filepath.Join(dir, "a.py")}})
		var summaries []string
		for _, c := range res.Checks {
			if c.Name != "tools" {
				t.Errorf("unexpected %s", c.Name)
			}
			summaries = append(summaries, c.Summary)
		}
		joined := strings.Join(summaries, "|")
		for _, want := range []string{"ruff is not installed", "uv is not installed", "mypy is not installed"} {
			if !strings.Contains(joined, want) {
				t.Errorf("missing %q in %s", want, joined)
			}
		}
	})
	t.Run("typecheck off and errors at gate", func(t *testing.T) {
		t.Parallel()
		g := newGate(t, nil)
		g.f.OnFunc("env", writeCov(covJSON(map[string][2][]int{"pkg/a.py": {{1}, nil}}), runner.Result{}))
		g.f.On("mypy", runner.Result{ExitCode: 1, Stdout: "pkg/other.py:1: error: Debt  [misc]\npkg/a.py:1: error: Bad  [misc]\n"})
		res := New().Gate(ctx, g.e, module.GateInput{Files: []string{g.a}})
		if c, ok := find(res.Checks, "mypy"); !ok || len(c.Issues) != 1 || c.Issues[0].File != "pkg/a.py" {
			t.Fatalf("mypy issues in touched files expected: %+v", res.Checks)
		}
		var mypyArgs string
		for _, call := range g.f.Calls() {
			if call.Name == "mypy" {
				mypyArgs = call.String()
			}
		}
		if !strings.HasSuffix(mypyArgs, filepath.Join(g.dir, "pkg")) {
			t.Fatalf("mypy should check the touched package: %s", mypyArgs)
		}
		g.e.Config.Python.Typecheck = "off"
		res = New().Gate(ctx, g.e, module.GateInput{Files: []string{g.a}})
		if len(res.Checks) != 0 || strings.Contains(strings.Join(res.Ran, ","), "mypy") {
			t.Fatalf("%+v %v", res.Checks, res.Ran)
		}
	})
	t.Run("uv lock check", func(t *testing.T) {
		t.Parallel()
		g := newGate(t, map[string]string{"uv.lock": ""})
		g.f.OnFunc("env", writeCov(covJSON(map[string][2][]int{"pkg/a.py": {{1}, nil}}), runner.Result{}))
		g.f.On("uv lock --check", runner.Result{ExitCode: 1, Stderr: "lockfile needs to be updated"})
		res := New().Gate(ctx, g.e, module.GateInput{Files: []string{g.a}})
		if c, ok := find(res.Checks, "lock"); !ok || c.Fix != "run `uv lock`" {
			t.Fatalf("%+v", res.Checks)
		}
		g.f.OnError("uv lock --check", errors.New("timeout"))
		res = New().Gate(ctx, g.e, module.GateInput{Files: []string{g.a}})
		if len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "timeout") {
			t.Fatal(res.Notes)
		}
	})
	t.Run("uv lock without uv uses venv tests", func(t *testing.T) {
		t.Parallel()
		dir := repo(t, map[string]string{"uv.lock": "", "a.py": "", ".venv/bin/python": "", ".venv/bin/ruff": "", ".venv/bin/mypy": ""})
		f := runner.NewFake().On(filepath.Join(dir, ".venv/bin/ruff")+" check", runner.Result{Stdout: "[]"})
		f.OnFunc("env", writeCov(covJSON(map[string][2][]int{"a.py": {{1}, nil}}), runner.Result{}))
		res := New().Gate(ctx, env(dir, f), module.GateInput{Files: []string{filepath.Join(dir, "a.py")}})
		if len(res.Checks) != 0 || len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "uv.lock check skipped") {
			t.Fatalf("%+v %v", res.Checks, res.Notes)
		}
	})
	t.Run("all mode", func(t *testing.T) {
		t.Parallel()
		g := newGate(t, map[string]string{"sub/pyproject.toml": "", "sub/m.py": "", ".venv/lib/x.py": "", "node_modules/y.py": ""})
		g.f.OnFunc("env", writeCov(covJSON(map[string][2][]int{"m.py": {{1}, nil}}), runner.Result{}))
		res := New().Gate(ctx, g.e, module.GateInput{All: true})
		dirs := map[string]bool{}
		for _, c := range g.f.Calls() {
			if c.Name == "env" {
				dirs[c.Dir] = true
			}
		}
		if !dirs[g.dir] || !dirs[filepath.Join(g.dir, "sub")] || len(dirs) != 2 {
			t.Fatalf("projects %v", dirs)
		}
		if res.Total != 100 {
			t.Fatal(res.Total)
		}
	})
}

func TestHelpers(t *testing.T) {
	t.Parallel()
	if got := ranges([]int{1, 2, 3, 5, 7, 8}); got != "1-3,5,7-8" {
		t.Error(got)
	}
	if got := capList([]string{"a", "b", "c"}, 2); strings.Join(got, ",") != "a,b,+1 more" {
		t.Error(got)
	}
	if got := appendUnique([]string{"a"}, "a", "b"); strings.Join(got, ",") != "a,b" {
		t.Error(got)
	}
	if got := lastLines("a\nb\nc", 2); strings.Join(got, ",") != "b,c" {
		t.Error(got)
	}
	if plural(1) != "" || plural(2) != "s" {
		t.Error("plural")
	}
	if displayPath("/r", "/elsewhere/x") != "/elsewhere/x" {
		t.Error("display outside repo")
	}
	if len(allLines("/does/not/exist")) != 0 {
		t.Error("allLines missing file")
	}
	if hasProjectTable("/does/not/exist") {
		t.Error("project table")
	}
	dir := repo(t, map[string]string{"setup.cfg": "", "a/b/c.py": "", "x/pyproject.toml": "", "x/y.py": ""})
	if got := projectRoot(dir, filepath.Join(dir, "a/b/c.py")); got != dir {
		t.Error(got)
	}
	if got := projectRoot(dir, filepath.Join(dir, "x/y.py")); got != filepath.Join(dir, "x") {
		t.Error(got)
	}
	bare := repo(t, map[string]string{"a/b.py": ""})
	if got := projectRoot(bare, filepath.Join(bare, "a/b.py")); got != bare {
		t.Error(got)
	}
	if got := projectRoot(bare, "/outside/z.py"); got != bare {
		t.Error(got)
	}
	if _, err := parseRuff("  "); err == nil {
		t.Error("empty ruff output")
	}
	if !isTestFile("conftest.py") || !isTestFile("x_test.py") || isTestFile("x.py") {
		t.Error("isTestFile")
	}
}

func FuzzParseMypy(f *testing.F) {
	f.Add("a.py:1:2: error: Bad  [misc]\n")
	f.Add("a.pyi:3: error: x\nnote")
	f.Fuzz(func(t *testing.T, s string) {
		for _, is := range parseMypy(s) {
			if is.File == "" || is.Line < 0 {
				t.Fatalf("bad issue %+v", is)
			}
		}
	})
}
