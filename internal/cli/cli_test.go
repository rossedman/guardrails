package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rossedman/guardrails/internal/config"
	"github.com/rossedman/guardrails/internal/feedback"
	"github.com/rossedman/guardrails/internal/module"
	"github.com/rossedman/guardrails/internal/runner"
	"github.com/rossedman/guardrails/internal/state"
)

// stub is a language module owning *.stub files with a scripted gate.
type stub struct {
	tools []module.Tool
	gate  module.GateResult
	in    []module.GateInput
}

func (*stub) Name() string               { return "stub" }
func (*stub) Enabled(config.Config) bool { return true }
func (*stub) Match(p string) bool        { return strings.HasSuffix(p, ".stub") }
func (s *stub) Tools() []module.Tool     { return s.tools }
func (s *stub) Standards(config.Config) string {
	return "Stub standards."
}

func (*stub) Fix(context.Context, module.Env, []string) module.FixResult {
	return module.FixResult{}
}

func (*stub) Check(context.Context, module.Env, []string) []feedback.Check { return nil }

func (s *stub) Gate(_ context.Context, _ module.Env, in module.GateInput) module.GateResult {
	s.in = append(s.in, in)
	return s.gate
}

type fixture struct {
	t      *testing.T
	app    *App
	repo   string
	data   string
	fake   *runner.Fake
	mod    *stub
	stdout *bytes.Buffer
	stderr *bytes.Buffer
	env    map[string]string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	repo := t.TempDir()
	data := t.TempDir()
	home := t.TempDir()
	fake := runner.NewFake()
	fake.On("git rev-parse --show-toplevel", runner.Result{Stdout: repo + "\n"})
	f := &fixture{
		t: t, repo: repo, data: data, fake: fake, mod: &stub{},
		stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{},
		env: map[string]string{"CLAUDE_PLUGIN_DATA": data, "HOME": home},
	}
	f.app = &App{
		Stdin:   strings.NewReader(""),
		Stdout:  f.stdout,
		Stderr:  f.stderr,
		Runner:  fake,
		Getenv:  func(k string) string { return f.env[k] },
		Getwd:   func() (string, error) { return repo, nil },
		Now:     func() time.Time { return time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC) },
		Modules: []module.Module{f.mod},
	}
	return f
}

func (f *fixture) run(stdin string, args ...string) int {
	f.t.Helper()
	f.stdout.Reset()
	f.stderr.Reset()
	f.app.Stdin = strings.NewReader(stdin)
	return f.app.Run(context.Background(), args)
}

func (f *fixture) write(rel, content string) string {
	f.t.Helper()
	p := filepath.Join(f.repo, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
	return p
}

func TestNewWiresRealProcess(t *testing.T) {
	a := New()
	if a.Stdout != os.Stdout || a.Stdin != os.Stdin || a.Getenv == nil || a.Getwd == nil || a.Now == nil {
		t.Errorf("New() = %+v", a)
	}
	if _, ok := a.Runner.(runner.Exec); !ok {
		t.Errorf("runner = %T", a.Runner)
	}
}

func TestBasicCommands(t *testing.T) {
	f := newFixture(t)
	if code := f.run(""); code != 2 || !strings.Contains(f.stderr.String(), "Usage:") {
		t.Errorf("no args: %d %q", code, f.stderr)
	}
	if code := f.run("", "frobnicate"); code != 2 || !strings.Contains(f.stderr.String(), `unknown command "frobnicate"`) {
		t.Errorf("unknown: %d %q", code, f.stderr)
	}
	for _, v := range []string{"version", "--version", "-v"} {
		if code := f.run("", v); code != 0 || !strings.HasPrefix(f.stdout.String(), "guardrails dev (commit none") {
			t.Errorf("%s: %d %q", v, code, f.stdout)
		}
	}
	for _, h := range []string{"help", "-h", "--help"} {
		if code := f.run("", h); code != 0 || !strings.Contains(f.stdout.String(), "Commands:") {
			t.Errorf("%s: %d", h, code)
		}
	}
	if code := f.run("", "schema"); code != 0 || !json.Valid(f.stdout.Bytes()) {
		t.Errorf("schema: %d valid=%v", code, json.Valid(f.stdout.Bytes()))
	}
}

func TestConfigCommand(t *testing.T) {
	f := newFixture(t)
	f.write(config.RepoFile, "mode: warn\n")
	if code := f.run("", "config"); code != 0 || !strings.Contains(f.stdout.String(), "mode: warn") {
		t.Errorf("valid: %d %q", code, f.stdout)
	}
	f.write(config.RepoFile, "mood: warn\n")
	if code := f.run("", "config"); code != 1 || !strings.Contains(f.stderr.String(), "invalid config") {
		t.Errorf("invalid: %d %q", code, f.stderr)
	}
	f.app.Getwd = func() (string, error) { return "", errors.New("no cwd") }
	if code := f.run("", "config"); code != 1 || !strings.Contains(f.stderr.String(), "no cwd") {
		t.Errorf("getwd error: %d %q", code, f.stderr)
	}
}

func TestHookCommand(t *testing.T) {
	f := newFixture(t)
	if code := f.run("", "hook"); code != 2 || !strings.Contains(f.stderr.String(), "usage: guardrails hook") {
		t.Errorf("no event: %d", code)
	}
	if code := f.run("", "hook", "nope"); code != 2 || !strings.Contains(f.stderr.String(), "unknown hook event") {
		t.Errorf("unknown event: %d", code)
	}
	if code := f.run("not json", "hook", "stop"); code != 2 || !strings.Contains(f.stderr.String(), "guardrails Stop:") {
		t.Errorf("bad json blocking: %d %q", code, f.stderr)
	}
	if code := f.run("not json", "hook", "session-start"); code != 1 {
		t.Errorf("bad json non-blocking: %d", code)
	}
	if code := f.run(`{"cwd":"/x"}`, "hook", "stop"); code != 2 || !strings.Contains(f.stderr.String(), "session_id") {
		t.Errorf("engine error: %d %q", code, f.stderr)
	}
	in := `{"session_id":"s1","cwd":"` + f.repo + `","hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"git reset --hard"}}`
	if code := f.run(in, "hook", "PreToolUse"); code != 0 || !strings.Contains(f.stdout.String(), `"permissionDecision":"deny"`) {
		t.Errorf("deny: %d %q", code, f.stdout)
	}
	f.app.Stdout = failWriter{}
	f.app.Stdin = strings.NewReader(in)
	if code := f.app.Run(context.Background(), []string{"hook", "pre-tool-use"}); code != 2 {
		t.Errorf("write failure: %d", code)
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

func TestGateCommand(t *testing.T) {
	failing := module.GateResult{
		Checks: []feedback.Check{{Name: "lint", Failed: true, Summary: "1 issue", Issues: []feedback.Issue{{File: "a.stub", Line: 1, Rule: "r", Message: "bad"}}}},
		Total:  -1, Ran: []string{"build", "lint"}, Notes: []string{"vuln skipped"},
	}
	t.Run("no changes", func(t *testing.T) {
		f := newFixture(t)
		if code := f.run("", "gate"); code != 0 || !strings.Contains(f.stdout.String(), "no changes against HEAD") {
			t.Errorf("%d %q", code, f.stdout)
		}
	})
	t.Run("bad flag", func(t *testing.T) {
		f := newFixture(t)
		if code := f.run("", "gate", "--bogus"); code != 2 {
			t.Errorf("code = %d", code)
		}
	})
	t.Run("getwd and config errors", func(t *testing.T) {
		f := newFixture(t)
		f.app.Getwd = func() (string, error) { return "", errors.New("gone") }
		if code := f.run("", "gate"); code != 1 {
			t.Errorf("getwd: %d", code)
		}
		f = newFixture(t)
		f.write(config.RepoFile, "x: 1\n")
		if code := f.run("", "gate"); code != 1 || !strings.Contains(f.stderr.String(), "invalid config") {
			t.Errorf("config: %d %q", code, f.stderr)
		}
	})
	t.Run("changed files pass", func(t *testing.T) {
		f := newFixture(t)
		f.fake.On("git diff --name-only", runner.Result{Stdout: "a.stub\nb.md\n"})
		f.fake.On("git ls-files --others", runner.Result{Stdout: "c.stub\na.stub\n"})
		f.mod.gate = module.GateResult{Total: 93.5, Coverage: map[string]float64{"p": 93.5}, Ran: []string{"build", "tests"}}
		_ = state.New(f.data).SaveRepo(f.repo, &state.RepoCache{Coverage: map[string]float64{"p": 90}})
		code := f.run("", "gate", "--base", "main")
		if code != 0 || !strings.Contains(f.stdout.String(), "stub gate passed (2 checks)") || !strings.Contains(f.stdout.String(), "coverage 93.5% (threshold 90%)") {
			t.Errorf("%d %q", code, f.stdout)
		}
		in := f.mod.in[0]
		if len(in.Files) != 2 || in.BaseCommit != "main" || in.Baseline["p"] != 90 || in.All {
			t.Errorf("gate input = %+v", in)
		}
		rc, _ := state.New(f.data).LoadRepo(f.repo)
		if rc.LastGate == nil || !rc.LastGate.Passed || rc.Coverage["p"] != 93.5 {
			t.Errorf("repo cache = %+v", rc)
		}
		// explain prints the saved report.
		if code := f.run("", "explain"); code != 0 || !strings.Contains(f.stdout.String(), "gate passed") {
			t.Errorf("explain: %d %q", code, f.stdout)
		}
	})
	t.Run("all fails", func(t *testing.T) {
		f := newFixture(t)
		f.mod.gate = failing
		code := f.run("", "gate", "--all")
		out := f.stdout.String()
		if code != 1 || !strings.Contains(out, "failed (1 of 2 checks)") || !strings.Contains(out, "a.stub:1  r  bad") {
			t.Errorf("%d %q", code, out)
		}
		if !strings.Contains(f.stderr.String(), "note: vuln skipped") {
			t.Errorf("stderr = %q", f.stderr)
		}
		if in := f.mod.in[0]; !in.All || in.BaseCommit != "" {
			t.Errorf("gate input = %+v", in)
		}
	})
	t.Run("explain prints full report", func(t *testing.T) {
		f := newFixture(t)
		c := feedback.Check{Name: "lint", Failed: true, Summary: "many"}
		for i := 0; i < 15; i++ {
			c.Issues = append(c.Issues, feedback.Issue{File: "a.stub", Line: i + 1, Message: "m"})
		}
		f.mod.gate = module.GateResult{Checks: []feedback.Check{c}, Total: -1}
		f.run("", "gate", "--all")
		if strings.Count(f.stdout.String(), "a.stub:") != 10 {
			t.Errorf("default output should cap at 10 issues: %q", f.stdout)
		}
		f.run("", "gate", "--all", "--explain")
		if strings.Count(f.stdout.String(), "a.stub:") != 15 {
			t.Errorf("--explain should show all 15: %q", f.stdout)
		}
	})
	t.Run("json", func(t *testing.T) {
		f := newFixture(t)
		f.mod.gate = failing
		code := f.run("", "gate", "--all", "--json")
		var got struct {
			Passed bool             `json:"passed"`
			Checks []feedback.Check `json:"checks"`
			Notes  []string         `json:"notes"`
		}
		if err := json.Unmarshal(f.stdout.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if code != 1 || got.Passed || len(got.Checks) != 1 || got.Notes[0] != "vuln skipped" || f.stderr.Len() != 0 {
			t.Errorf("%d %+v stderr=%q", code, got, f.stderr)
		}
	})
}

func TestPassFail(t *testing.T) {
	if got := passFail(true, 0, 4); got != "passed (4 checks)" {
		t.Error(got)
	}
	if got := passFail(false, 2, 1); got != "failed (2 of 2 checks)" {
		t.Error(got)
	}
}

func TestExplainErrors(t *testing.T) {
	f := newFixture(t)
	if code := f.run("", "explain"); code != 1 || !strings.Contains(f.stderr.String(), "no gate has run") {
		t.Errorf("no gate: %d %q", code, f.stderr)
	}
	_ = state.New(f.data).SaveRepo(f.repo, &state.RepoCache{LastReport: filepath.Join(f.data, "missing.txt")})
	if code := f.run("", "explain"); code != 1 || !strings.Contains(f.stderr.String(), "read last report") {
		t.Errorf("missing report: %d %q", code, f.stderr)
	}
	p := filepath.Join(f.data, "repos", state.Key(f.repo)+".json")
	_ = os.WriteFile(p, []byte("{bad"), 0o600)
	if code := f.run("", "explain"); code != 1 {
		t.Errorf("corrupt cache: %d", code)
	}
	f.app.Getwd = func() (string, error) { return "", errors.New("gone") }
	if code := f.run("", "explain"); code != 1 {
		t.Errorf("getwd: %d", code)
	}
}

func TestDoctor(t *testing.T) {
	f := newFixture(t)
	f.mod.tools = []module.Tool{
		{Name: "req", Required: true, VersionArgs: []string{"--version"}},
		{Name: "opt", Install: "get opt"},
		{Name: "quiet", VersionArgs: []string{"version"}},
	}
	f.fake.Install("req", "quiet")
	f.fake.On("req --version", runner.Result{Stdout: "req 1.2.3\nextra\n"})
	f.fake.OnError("quiet version", errors.New("boom"))
	code := f.run("", "doctor")
	out := f.stdout.String()
	if code != 0 || !strings.Contains(out, "req 1.2.3") || !strings.Contains(out, "✗ opt") || !strings.Contains(out, "config:   ✓") {
		t.Errorf("all required present: %d\n%s", code, out)
	}
	f.mod.tools = append(f.mod.tools, module.Tool{Name: "missing", Required: true, Install: "get it"})
	if code := f.run("", "doctor"); code != 1 || !strings.Contains(f.stdout.String(), "required, not found — get it") {
		t.Errorf("required missing: %d\n%s", code, f.stdout)
	}
	f.mod.tools = nil
	f.write(config.RepoFile, "bad: key\n")
	if code := f.run("", "doctor"); code != 1 || !strings.Contains(f.stdout.String(), "config:   ✗") {
		t.Errorf("bad config: %d\n%s", code, f.stdout)
	}
}

func TestInit(t *testing.T) {
	f := newFixture(t)
	if code := f.run("", "init"); code != 0 {
		t.Fatalf("init: %d %q", code, f.stderr)
	}
	for _, rel := range []string{config.RepoFile, ".claude/rules/go.md"} {
		if _, err := os.Stat(filepath.Join(f.repo, rel)); err != nil {
			t.Errorf("%s not written: %v", rel, err)
		}
	}
	if !strings.Contains(f.stdout.String(), `"permissions"`) {
		t.Error("settings snippet not printed")
	}
	if _, err := config.Load("", f.repo); err != nil {
		t.Errorf("written config does not load: %v", err)
	}
	f.write(config.RepoFile, "version: 1\n")
	f.run("", "init")
	if !strings.Contains(f.stdout.String(), "skip   .guardrails.yaml") {
		t.Errorf("existing file should be skipped: %q", f.stdout)
	}
	f.run("", "init", "--force", "--rules=false")
	data, _ := os.ReadFile(filepath.Join(f.repo, config.RepoFile))
	if !strings.Contains(string(data), "patch_coverage") || strings.Contains(f.stdout.String(), "rules/go.md") {
		t.Errorf("--force/--rules=false: %q", f.stdout)
	}
	if code := f.run("", "init", "--nope"); code != 1 {
		t.Errorf("bad flag: %d", code)
	}
	f.app.Getwd = func() (string, error) { return "", errors.New("gone") }
	if code := f.run("", "init"); code != 1 {
		t.Errorf("getwd: %d", code)
	}
}

func TestInitWriteErrors(t *testing.T) {
	f := newFixture(t)
	// A file where the .claude directory should be breaks the rules write.
	f.write(".claude", "not a dir")
	if code := f.run("", "init"); code != 1 {
		t.Errorf("rules under a file: %d", code)
	}
	f = newFixture(t)
	if err := os.Mkdir(filepath.Join(f.repo, config.RepoFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if code := f.run("", "init", "--force"); code != 1 {
		t.Errorf("config path is a dir: %d", code)
	}
}

func TestStatusline(t *testing.T) {
	f := newFixture(t)
	f.run("", "statusline")
	if got := f.stdout.String(); got != "⛨ guardrails: no gate yet\n" {
		t.Errorf("no gate: %q", got)
	}
	store := state.New(f.data)
	_ = store.SaveRepo(f.repo, &state.RepoCache{LastGate: &state.GateSummary{Passed: true, Coverage: 91.25, Threshold: 90, Issues: 0}})
	f.run(`{"workspace":{"current_dir":"`+f.repo+`"}}`, "statusline")
	if got := f.stdout.String(); got != "⛨ gate ✓ · cov 91.2%/90% · lint 0\n" {
		t.Errorf("passed: %q", got)
	}
	_ = store.SaveRepo(f.repo, &state.RepoCache{LastGate: &state.GateSummary{Passed: false, Coverage: -1, Issues: 3}})
	f.run(`{"cwd":"`+f.repo+`"}`, "statusline")
	if got := f.stdout.String(); got != "⛨ gate ✗ · cov n/a · lint 3\n" {
		t.Errorf("failed: %q", got)
	}
	f.app.Stdin = nil
	f.stdout.Reset()
	f.app.Run(context.Background(), []string{"statusline"})
	if !strings.Contains(f.stdout.String(), "gate ✗") {
		t.Errorf("nil stdin: %q", f.stdout)
	}
}

func TestDataDirAndGlobalConfig(t *testing.T) {
	f := newFixture(t)
	f.env = map[string]string{"CLAUDE_PLUGIN_DATA": "/plugin"}
	if got := f.app.DataDir(); got != "/plugin" {
		t.Errorf("plugin: %q", got)
	}
	f.env = map[string]string{"XDG_DATA_HOME": "/xdg", "XDG_CONFIG_HOME": "/xcfg"}
	if got := f.app.DataDir(); got != filepath.Join("/xdg", "guardrails") {
		t.Errorf("xdg: %q", got)
	}
	if got := f.app.globalConfig(); got != filepath.Join("/xcfg", "guardrails", "config.yaml") {
		t.Errorf("xdg config: %q", got)
	}
	f.env = map[string]string{"HOME": "/h"}
	if got := f.app.DataDir(); got != filepath.Join("/h", ".local", "share", "guardrails") {
		t.Errorf("home: %q", got)
	}
	if got := f.app.globalConfig(); got != filepath.Join("/h", ".config", "guardrails", "config.yaml") {
		t.Errorf("home config: %q", got)
	}
	f.env = map[string]string{}
	if got := f.app.DataDir(); got != filepath.Join(os.TempDir(), "guardrails") {
		t.Errorf("tmp: %q", got)
	}
	if got := f.app.globalConfig(); got != "" {
		t.Errorf("no config: %q", got)
	}
}

func TestDefaultModulesUsedWithoutOverride(t *testing.T) {
	f := newFixture(t)
	f.app.Modules = nil
	if code := f.run("", "doctor"); code != 1 || !strings.Contains(f.stdout.String(), "go module") {
		t.Errorf("%d %q", code, f.stdout)
	}
}

func TestTrimLines(t *testing.T) {
	got := trimLines("  a \n\n b\n")
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("%q", got)
	}
}
