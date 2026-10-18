package yamlmod

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rossedman/guardrails/internal/config"
	"github.com/rossedman/guardrails/internal/feedback"
	"github.com/rossedman/guardrails/internal/module"
	"github.com/rossedman/guardrails/internal/runner"
	"github.com/rossedman/guardrails/internal/tools"
)

// resolver answers tool lookups from a fixed table; absent tools are
// missing.
type resolver map[string]error

func (r resolver) Resolve(_ context.Context, name string) (tools.Command, error) {
	err, ok := r[name]
	if !ok {
		return tools.Command{}, fmt.Errorf("%s: %w", name, tools.ErrMissing)
	}
	if err != nil {
		return tools.Command{}, fmt.Errorf("%s: %w", name, err)
	}
	return tools.Command{Path: name}, nil
}

func allTools() resolver {
	return resolver{"yamllint": nil, "actionlint": nil, "kubeconform": nil}
}

const pod = "apiVersion: v1\nkind: Pod\nmetadata:\n  name: x\n"

type fixture struct {
	repo  string
	fake  *runner.Fake
	env   module.Env
	files map[string]string
}

func newFixture(t *testing.T, files map[string]string) *fixture {
	t.Helper()
	repo := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fake := runner.NewFake()
	return &fixture{
		repo: repo, fake: fake, files: files,
		env: module.Env{Runner: fake, Config: config.Default(), Repo: repo, DataDir: filepath.Join(repo, ".data"), Tools: allTools()},
	}
}

func (f *fixture) path(rel string) string { return filepath.Join(f.repo, filepath.FromSlash(rel)) }

func checkNamed(checks []feedback.Check, name string) (feedback.Check, bool) {
	for _, c := range checks {
		if c.Name == name {
			return c, true
		}
	}
	return feedback.Check{}, false
}

func TestBasics(t *testing.T) {
	t.Parallel()
	m := New()
	if m.Name() != "yaml" {
		t.Errorf("Name = %q", m.Name())
	}
	for path, want := range map[string]bool{
		"a.yml": true, "b/c.yaml": true, "D.YAML": true, "x.json": false, "yaml": false, "a.yml.bak": false,
	} {
		if got := m.Match(path); got != want {
			t.Errorf("Match(%q) = %v, want %v", path, got, want)
		}
	}
	cfg := config.Default()
	if !m.Enabled(cfg) {
		t.Error("enabled by default")
	}
	off := false
	cfg.YAML.Enabled = &off
	if m.Enabled(cfg) {
		t.Error("enabled: false must disable")
	}
	names := map[string]bool{}
	for _, tl := range m.Tools() {
		names[tl.Name] = true
	}
	for _, n := range []string{"yamllint", "actionlint", "kubeconform"} {
		if !names[n] {
			t.Errorf("Tools missing %s", n)
		}
	}
	if got := m.tool("nope"); got.Name != "nope" {
		t.Errorf("tool fallback = %+v", got)
	}
	if res := m.Fix(context.Background(), module.Env{}, []string{"a.yml"}); len(res.Notes) != 0 {
		t.Errorf("Fix should do nothing: %+v", res)
	}
}

func TestStandards(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	got := New().Standards(cfg)
	for _, want := range []string{"yamllint", "actionlint", "kubeconform", "1.31"} {
		if !strings.Contains(got, want) {
			t.Errorf("Standards missing %q: %s", want, got)
		}
	}
	cfg.YAML.Actionlint = false
	cfg.YAML.Kubeconform.Enabled = false
	got = New().Standards(cfg)
	for _, absent := range []string{"actionlint", "kubeconform"} {
		if strings.Contains(got, absent) {
			t.Errorf("Standards should not mention %q: %s", absent, got)
		}
	}
}

func TestYamllint(t *testing.T) {
	t.Parallel()
	out := strings.Join([]string{
		"x.yaml:2:1: [error] duplication of key \"a\" in mapping (key-duplicates)",
		"x.yaml:3:5: [warning] too many spaces after colon (colons)",
		"x.yaml:4:1: [error] syntax error: mapping values are not allowed here",
		"not a parsable line",
	}, "\n")
	tests := []struct {
		name       string
		config     bool
		res        runner.Result
		err        error
		wantFailed bool
		wantIssues int
		wantRules  []string
		wantArgs   string
		wantSumm   string
	}{
		{
			name: "errors only, relaxed without config", res: runner.Result{Stdout: out, ExitCode: 1},
			wantFailed: true, wantIssues: 2, wantRules: []string{"key-duplicates", "yamllint"},
			wantArgs: "yamllint -f parsable -d relaxed x.yaml", wantSumm: "2 errors",
		},
		{
			name: "repo config respected", config: true, res: runner.Result{Stdout: out, ExitCode: 1},
			wantFailed: true, wantIssues: 2, wantArgs: "yamllint -f parsable x.yaml",
		},
		{
			name: "warnings only pass", res: runner.Result{Stdout: "x.yaml:3:5: [warning] too many spaces (colons)\n"},
			wantArgs: "yamllint -f parsable -d relaxed x.yaml",
		},
		{
			name: "crash without output fails", res: runner.Result{Stderr: "Traceback: boom", ExitCode: 2},
			wantFailed: true, wantSumm: "yamllint failed to run",
		},
		{name: "run error", err: errors.New("exec failed"), wantFailed: true, wantSumm: "could not run"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			files := map[string]string{"x.yaml": "a: 1\n"}
			if tc.config {
				files[".yamllint.yaml"] = "extends: default\n"
			}
			f := newFixture(t, files)
			if tc.err != nil {
				f.fake.OnError("yamllint", tc.err)
			} else {
				f.fake.On("yamllint", tc.res)
			}
			checks := New().Check(context.Background(), f.env, []string{f.path("x.yaml")})
			c, failed := checkNamed(checks, "yamllint")
			if failed != tc.wantFailed {
				t.Fatalf("failed = %v, want %v (%+v)", failed, tc.wantFailed, checks)
			}
			if tc.wantArgs != "" && !f.fake.Ran(tc.wantArgs) {
				t.Errorf("calls = %v, want %q", f.fake.Calls(), tc.wantArgs)
			}
			if !failed {
				return
			}
			if len(c.Issues) != tc.wantIssues {
				t.Errorf("issues = %+v, want %d", c.Issues, tc.wantIssues)
			}
			for i, r := range tc.wantRules {
				if c.Issues[i].Rule != r || c.Issues[i].File != "x.yaml" {
					t.Errorf("issue %d = %+v, want rule %s", i, c.Issues[i], r)
				}
			}
			if tc.wantSumm != "" && !strings.Contains(c.Summary, tc.wantSumm) {
				t.Errorf("summary = %q, want %q", c.Summary, tc.wantSumm)
			}
		})
	}
}

func TestActionlint(t *testing.T) {
	t.Parallel()
	wf := ".github/workflows/ci.yml"
	issue := `[{"message":"property \"foo\" is not defined","filepath":".github/workflows/ci.yml","line":6,"column":23,"kind":"expression"},` +
		`{"message":"bad","filepath":".github/workflows/ci.yml","line":7,"column":1,"kind":""}]`
	tests := []struct {
		name       string
		file       string
		disabled   bool
		res        runner.Result
		err        error
		wantRan    bool
		wantFailed bool
		wantSumm   string
	}{
		{name: "issues", file: wf, res: runner.Result{Stdout: issue, ExitCode: 1}, wantRan: true, wantFailed: true, wantSumm: "2 issues"},
		{name: "clean", file: wf, res: runner.Result{Stdout: "[]"}, wantRan: true},
		{name: "null output", file: wf, res: runner.Result{Stdout: "null"}, wantRan: true},
		{name: "empty output", file: wf, res: runner.Result{}, wantRan: true},
		{name: "unparsable", file: wf, res: runner.Result{Stdout: "{nope", ExitCode: 1}, wantRan: true, wantFailed: true, wantSumm: "could not parse"},
		{name: "fatal exit", file: wf, res: runner.Result{Stderr: "config error", ExitCode: 3}, wantRan: true, wantFailed: true, wantSumm: "failed to run"},
		{name: "run error", file: wf, err: errors.New("boom"), wantRan: true, wantFailed: true, wantSumm: "could not run"},
		{name: "not a workflow", file: "deploy/ci.yml", res: runner.Result{Stdout: issue}},
		{name: "nested under workflows is not a workflow", file: ".github/workflows/sub/x.yml", res: runner.Result{Stdout: issue}},
		{name: "disabled", file: wf, disabled: true, res: runner.Result{Stdout: issue}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, map[string]string{tc.file: "on: push\n"})
			f.env.Config.YAML.Actionlint = !tc.disabled
			if tc.err != nil {
				f.fake.OnError("actionlint", tc.err)
			} else {
				f.fake.On("actionlint", tc.res)
			}
			checks := New().Check(context.Background(), f.env, []string{f.path(tc.file)})
			if ran := f.fake.Ran("actionlint -format {{json .}} " + tc.file); ran != tc.wantRan {
				t.Errorf("ran actionlint = %v, want %v (%v)", ran, tc.wantRan, f.fake.Calls())
			}
			c, failed := checkNamed(checks, "actionlint")
			if failed != tc.wantFailed {
				t.Fatalf("failed = %v, want %v (%+v)", failed, tc.wantFailed, checks)
			}
			if failed && !strings.Contains(c.Summary, tc.wantSumm) {
				t.Errorf("summary = %q, want %q", c.Summary, tc.wantSumm)
			}
			if tc.name == "issues" {
				if c.Issues[0].Rule != "expression" || c.Issues[0].Line != 6 || c.Issues[0].File != wf || c.Issues[1].Rule != "actionlint" {
					t.Errorf("issues = %+v", c.Issues)
				}
			}
		})
	}
}

func TestKubeconform(t *testing.T) {
	t.Parallel()
	invalid := `{"resources":[
	  {"filename":"pod.yaml","kind":"Pod","name":"x","status":"statusInvalid","msg":"m",
	   "validationErrors":[{"path":"/spec/containers/0","msg":"additional properties 'bogus' not allowed"},{"path":"","msg":"root problem"}]},
	  {"filename":"pod.yaml","kind":"ConfigMap","name":"y","status":"statusError","msg":"error unmarshalling resource"},
	  {"filename":"pod.yaml","kind":"Service","name":"z","status":"statusError","msg":"failed downloading schema at https://x: timeout"},
	  {"filename":"pod.yaml","kind":"Secret","name":"s","status":"statusValid"}
	]}`
	offline := `{"resources":[{"filename":"pod.yaml","kind":"Pod","name":"x","status":"statusError","msg":"failed downloading schema at https://x"}]}`
	tests := []struct {
		name       string
		file       string
		content    string
		disabled   bool
		version    string
		res        runner.Result
		err        error
		wantRan    bool
		wantFailed bool
		wantIssues int
		wantNotes  int
		wantVer    string
	}{
		{name: "violations and offline", file: "pod.yaml", content: pod, res: runner.Result{Stdout: invalid, ExitCode: 1}, wantRan: true, wantFailed: true, wantIssues: 3, wantNotes: 1, wantVer: "1.31.0"},
		{name: "offline only is a note", file: "pod.yaml", content: pod, res: runner.Result{Stdout: offline, ExitCode: 1}, wantRan: true, wantNotes: 1},
		{name: "valid", file: "pod.yaml", content: pod, res: runner.Result{Stdout: `{"resources":[]}`}, wantRan: true},
		{name: "full version kept", file: "pod.yaml", content: pod, version: "1.30.2", res: runner.Result{Stdout: `{"resources":[]}`}, wantRan: true, wantVer: "1.30.2"},
		{name: "master", file: "pod.yaml", content: pod, version: "master", res: runner.Result{Stdout: `{"resources":[]}`}, wantRan: true, wantVer: "master"},
		{name: "unparsable", file: "pod.yaml", content: pod, res: runner.Result{Stdout: "invalid flag", ExitCode: 1}, wantRan: true, wantFailed: true},
		{name: "run error", file: "pod.yaml", content: pod, err: errors.New("boom"), wantRan: true, wantFailed: true},
		{name: "second document is a manifest", file: "multi.yaml", content: "just: data\n---\n- a list\n---\n" + pod, res: runner.Result{Stdout: `{"resources":[]}`}, wantRan: true},
		{name: "plain yaml", file: "values.yaml", content: "replicas: 3\n"},
		{name: "invalid yaml is not a manifest", file: "bad.yaml", content: "a: [\n"},
		{name: "helm template skipped", file: "chart/templates/pod.yaml", content: "apiVersion: v1\nkind: Pod\nmetadata:\n  name: {{ .Release.Name }}\n"},
		{name: "disabled", file: "pod.yaml", content: pod, disabled: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, map[string]string{tc.file: tc.content})
			f.env.Config.YAML.Kubeconform.Enabled = !tc.disabled
			if tc.version != "" {
				f.env.Config.YAML.Kubeconform.KubernetesVersion = tc.version
			}
			if tc.err != nil {
				f.fake.OnError("kubeconform", tc.err)
			} else {
				f.fake.On("kubeconform", tc.res)
			}
			res := New().Gate(context.Background(), f.env, module.GateInput{Files: []string{f.path(tc.file)}})
			if ran := f.fake.Ran("kubeconform"); ran != tc.wantRan {
				t.Fatalf("ran kubeconform = %v, want %v", ran, tc.wantRan)
			}
			if tc.wantVer != "" && !f.fake.Ran("kubeconform -strict -summary=false -output json -ignore-missing-schemas -kubernetes-version "+tc.wantVer+" -cache") {
				t.Errorf("calls = %v, want version %s", f.fake.Calls(), tc.wantVer)
			}
			c, failed := checkNamed(res.Checks, "kubeconform")
			if failed != tc.wantFailed {
				t.Fatalf("failed = %v, want %v (%+v)", failed, tc.wantFailed, res.Checks)
			}
			if tc.wantIssues > 0 && len(c.Issues) != tc.wantIssues {
				t.Errorf("issues = %+v", c.Issues)
			}
			if tc.name == "violations and offline" {
				if c.Issues[0].Rule != "Pod/x" || !strings.HasPrefix(c.Issues[0].Message, "/spec/containers/0: ") || !strings.HasPrefix(c.Issues[1].Message, "/: ") {
					t.Errorf("issues = %+v", c.Issues)
				}
			}
			if len(res.Notes) != tc.wantNotes {
				t.Errorf("notes = %v, want %d", res.Notes, tc.wantNotes)
			}
		})
	}
}

func TestToolAvailability(t *testing.T) {
	t.Parallel()
	files := map[string]string{"x.yaml": "a: 1\n", ".github/workflows/ci.yml": "on: push\n", "pod.yaml": pod}
	targets := func(f *fixture) []string {
		return []string{f.path("x.yaml"), f.path(".github/workflows/ci.yml"), f.path("pod.yaml")}
	}

	t.Run("edit skips unavailable tools silently", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, files)
		f.env.Tools = resolver{"yamllint": tools.ErrInstalling}
		if checks := New().Check(context.Background(), f.env, targets(f)); len(checks) != 0 {
			t.Errorf("checks = %+v, want none", checks)
		}
		if len(f.fake.Calls()) != 0 {
			t.Errorf("no tool should run: %v", f.fake.Calls())
		}
	})

	t.Run("gate fails closed on unavailable tools", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, files)
		f.env.Tools = resolver{"yamllint": nil}
		f.fake.On("yamllint", runner.Result{})
		res := New().Gate(context.Background(), f.env, module.GateInput{Files: targets(f)})
		c, ok := checkNamed(res.Checks, "tools")
		if !ok {
			t.Fatalf("want tools check: %+v", res.Checks)
		}
		for _, want := range []string{"actionlint is not installed", "kubeconform is not installed"} {
			if !strings.Contains(c.Summary, want) {
				t.Errorf("summary %q missing %q", c.Summary, want)
			}
		}
		if len(res.Ran) != 1 || res.Ran[0] != "yamllint" {
			t.Errorf("Ran = %v", res.Ran)
		}
	})

	t.Run("gate runs everything when available", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, files)
		f.fake.On("kubeconform", runner.Result{Stdout: `{"resources":[]}`})
		res := New().Gate(context.Background(), f.env, module.GateInput{Files: targets(f)})
		if len(res.Checks) != 0 || len(res.Ran) != 3 || res.Total != -1 {
			t.Errorf("res = %+v", res)
		}
	})

	t.Run("PATH fallback without a resolver", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, map[string]string{"x.yaml": "a: 1\n"})
		f.env.Tools = nil
		f.fake.Install("yamllint")
		res := New().Gate(context.Background(), f.env, module.GateInput{Files: []string{f.path("x.yaml")}})
		if !f.fake.Ran("yamllint") || len(res.Checks) != 0 {
			t.Errorf("res = %+v calls = %v", res, f.fake.Calls())
		}
	})
}

func TestGateFiles(t *testing.T) {
	t.Parallel()
	t.Run("no files", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, nil)
		res := New().Gate(context.Background(), f.env, module.GateInput{Files: []string{f.path("gone.yaml")}})
		if len(res.Ran) != 0 || len(f.fake.Calls()) != 0 {
			t.Errorf("deleted files must be skipped: %+v", res)
		}
	})
	t.Run("all", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, map[string]string{
			"a.yaml": "a: 1\n", "sub/b.yml": "b: 2\n", ".github/workflows/ci.yml": "on: push\n",
			".hidden/c.yaml": "c: 3\n", "vendor/d.yaml": "d: 4\n", "node_modules/e.yaml": "e: 5\n", "f.txt": "x",
		})
		New().Gate(context.Background(), f.env, module.GateInput{All: true})
		if !f.fake.Ran("yamllint -f parsable -d relaxed .github/workflows/ci.yml a.yaml sub/b.yml") {
			t.Errorf("calls = %v", f.fake.Calls())
		}
	})
	t.Run("data dir unwritable skips cache", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, map[string]string{"pod.yaml": pod, "blocker": "file"})
		f.env.DataDir = filepath.Join(f.repo, "blocker")
		f.fake.On("kubeconform", runner.Result{Stdout: `{"resources":[]}`})
		New().Gate(context.Background(), f.env, module.GateInput{Files: []string{f.path("pod.yaml")}})
		for _, c := range f.fake.Calls() {
			if c.Name == "kubeconform" && strings.Contains(c.String(), "-cache") {
				t.Errorf("cache flag with unwritable data dir: %v", c)
			}
		}
	})
}

func TestHelpers(t *testing.T) {
	t.Parallel()
	if got := display("/repo", "/elsewhere/x.yaml"); got != "/elsewhere/x.yaml" {
		t.Errorf("display outside repo = %q", got)
	}
	if got := display("/repo", "rel/x.yaml"); got != "rel/x.yaml" {
		t.Errorf("display relative = %q", got)
	}
	if got := k8sVersion(""); got != "master" {
		t.Errorf("k8sVersion empty = %q", got)
	}
	if got := countSummary(1, "error"); got != "1 error" {
		t.Errorf("countSummary = %q", got)
	}
	if got := firstLines("a\n\n b \nc", 2); len(got) != 2 || got[1] != " b " {
		t.Errorf("firstLines = %q", got)
	}
	if isManifest(filepath.Join(t.TempDir(), "missing.yaml")) {
		t.Error("missing file is not a manifest")
	}
	if err := skipUnreadable(nil); err != nil {
		t.Errorf("skipUnreadable(nil) = %v", err)
	}
	dir := t.TempDir()
	entries, err := os.ReadDir(filepath.Dir(dir))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			if err := skipUnreadable(e); !errors.Is(err, filepath.SkipDir) {
				t.Errorf("skipUnreadable(dir) = %v", err)
			}
			break
		}
	}
}

// TestIntegration runs the real tools when they are on PATH.
func TestIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	for _, tool := range []string{"yamllint", "actionlint"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}
	repo := t.TempDir()
	write := func(rel, content string) string {
		p := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	dup := write("x.yaml", "a: 1\na: 2\nb:   3\n")
	wf := write(".github/workflows/ci.yml", "on: push\njobs:\n  x:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo ${{ github.nope }}\n")
	clean := write("ok.yaml", "a: 1\n")

	cfg := config.Default()
	cfg.YAML.Kubeconform.Enabled = false
	env := module.Env{Runner: runner.Exec{}, Config: cfg, Repo: repo}
	res := New().Gate(context.Background(), env, module.GateInput{Files: []string{dup, wf, clean}})

	yl, ok := checkNamed(res.Checks, "yamllint")
	if !ok || len(yl.Issues) != 1 || yl.Issues[0].Rule != "key-duplicates" || yl.Issues[0].File != "x.yaml" {
		t.Errorf("yamllint = %+v (warnings must not count)", yl)
	}
	al, ok := checkNamed(res.Checks, "actionlint")
	if !ok || len(al.Issues) == 0 || al.Issues[0].File != ".github/workflows/ci.yml" || al.Issues[0].Line != 6 {
		t.Errorf("actionlint = %+v", al)
	}
}
