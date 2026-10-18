package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rossedman/guardrails/internal/config"
	"github.com/rossedman/guardrails/internal/feedback"
	"github.com/rossedman/guardrails/internal/hook"
	"github.com/rossedman/guardrails/internal/module"
	"github.com/rossedman/guardrails/internal/runlog"
	"github.com/rossedman/guardrails/internal/runner"
	"github.com/rossedman/guardrails/internal/state"
)

var update = flag.Bool("update", false, "rewrite golden files in testdata/golden")

var fixedNow = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

// stub is a scripted language module owning *.stub files.
type stub struct {
	enabled   bool
	tools     []module.Tool
	fixNotes  []string
	checks    []feedback.Check
	gate      module.GateResult
	checkRuns int
	gateRuns  int
	gateFiles [][]string
	gateIn    []module.GateInput
}

func newStub() *stub { return &stub{enabled: true} }

func (s *stub) Name() string               { return "stub" }
func (s *stub) Enabled(config.Config) bool { return s.enabled }
func (s *stub) Match(path string) bool     { return strings.HasSuffix(path, ".stub") }
func (s *stub) Tools() []module.Tool       { return s.tools }
func (s *stub) Fix(context.Context, module.Env, []string) module.FixResult {
	return module.FixResult{Notes: s.fixNotes}
}

func (s *stub) Check(context.Context, module.Env, []string) []feedback.Check {
	s.checkRuns++
	return s.checks
}

func (s *stub) Gate(_ context.Context, _ module.Env, in module.GateInput) module.GateResult {
	s.gateRuns++
	s.gateFiles = append(s.gateFiles, in.Files)
	s.gateIn = append(s.gateIn, in)
	return s.gate
}

type harness struct {
	t    *testing.T
	repo string
	data string
	fake *runner.Fake
	mod  *stub
	eng  *Engine
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	repo := t.TempDir()
	data := t.TempDir()
	fake := runner.NewFake("tool-a")
	fake.On("git rev-parse --show-toplevel", runner.Result{Stdout: repo + "\n"})
	fake.On("git rev-parse HEAD", runner.Result{Stdout: "abc123\n"})
	fake.On("git rev-parse --abbrev-ref HEAD", runner.Result{Stdout: "feature\n"})
	mod := newStub()
	e := New(state.New(data), fake, "")
	e.Modules = []module.Module{mod}
	e.Now = func() time.Time { return fixedNow }
	h := &harness{t: t, repo: repo, data: data, fake: fake, mod: mod, eng: e}
	h.write("main.stub", "content\n")
	return h
}

func (h *harness) write(rel, content string) string {
	h.t.Helper()
	p := filepath.Join(h.repo, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		h.t.Fatal(err)
	}
	return p
}

func (h *harness) config(yaml string) { h.write(config.RepoFile, yaml) }

func (h *harness) in(event string, fields map[string]any) hook.Input {
	h.t.Helper()
	m := map[string]any{"session_id": "s1", "cwd": h.repo, "hook_event_name": event}
	for k, v := range fields {
		m[k] = v
	}
	data, err := json.Marshal(m)
	if err != nil {
		h.t.Fatal(err)
	}
	in, err := hook.ReadInput(bytes.NewReader(data))
	if err != nil {
		h.t.Fatal(err)
	}
	return in
}

func (h *harness) handle(event string, in hook.Input) hook.Output {
	h.t.Helper()
	out, err := h.eng.Handle(context.Background(), event, in)
	if err != nil {
		h.t.Fatalf("Handle(%s): %v", event, err)
	}
	return out
}

func (h *harness) session() *state.Session {
	h.t.Helper()
	s, err := h.eng.Store.Load("s1")
	if err != nil {
		h.t.Fatal(err)
	}
	return s
}

// payload loads testdata/payloads/name.json, substituting $REPO.
func (h *harness) payload(name string) hook.Input {
	h.t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "payloads", name+".json"))
	if err != nil {
		h.t.Fatal(err)
	}
	data = bytes.ReplaceAll(data, []byte("$REPO"), []byte(h.repo))
	in, err := hook.ReadInput(bytes.NewReader(data))
	if err != nil {
		h.t.Fatal(err)
	}
	return in
}

// golden compares the hook's stdout bytes with testdata/golden/name.json,
// with the temp paths replaced by placeholders.
func (h *harness) golden(name string, out hook.Output) {
	h.t.Helper()
	var buf bytes.Buffer
	if err := out.Write(&buf); err != nil {
		h.t.Fatal(err)
	}
	got := strings.ReplaceAll(buf.String(), h.repo, "$REPO")
	got = strings.ReplaceAll(got, h.data, "$DATA")
	path := filepath.Join("testdata", "golden", name+".json")
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			h.t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			h.t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		h.t.Fatalf("missing golden %s (run go test -update): %v", path, err)
	}
	if got != string(want) {
		h.t.Errorf("golden %s mismatch\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

var lintFail = feedback.Check{
	Name: "lint", Failed: true, Summary: "1 issue",
	Issues: []feedback.Issue{{File: "main.stub", Line: 3, Col: 1, Rule: "rule", Message: "bad thing"}},
	Fix:    "fix it",
}

// TestGolden drives each event from a recorded payload and compares the
// exact stdout against a golden file.
func TestGolden(t *testing.T) {
	cases := []struct {
		name    string
		event   string
		payload string
		setup   func(h *harness)
	}{
		{name: "session_start", event: hook.SessionStart, payload: "session_start", setup: func(h *harness) {
			h.mod.tools = []module.Tool{
				{Name: "tool-a", Required: true},
				{Name: "tool-b", Required: true, Install: "get tool-b"},
				{Name: "tool-c", Install: "get tool-c"},
			}
		}},
		{name: "pre_edit_suppression", event: hook.PreToolUse, payload: "pre_edit_suppression", setup: func(h *harness) {
			h.write("calc.go", "package calc\n\nfunc F() {\n\tg()\n}\n")
		}},
		{name: "pre_write_protected", event: hook.PreToolUse, payload: "pre_write_protected"},
		{name: "pre_bash_terraform", event: hook.PreToolUse, payload: "pre_bash_terraform"},
		{name: "pre_bash_allowed", event: hook.PreToolUse, payload: "pre_bash_allowed"},
		{name: "post_edit_block", event: hook.PostToolUse, payload: "post_edit", setup: func(h *harness) {
			h.mod.checks = []feedback.Check{lintFail}
		}},
		{name: "post_edit_clean", event: hook.PostToolUse, payload: "post_edit"},
		{name: "stop_block", event: hook.Stop, payload: "stop", setup: func(h *harness) {
			h.mod.gate = module.GateResult{
				Checks: []feedback.Check{{Name: "coverage", Failed: true, Summary: "50.0% < 90.0%", Lines: []string{"main.stub:1  F  0/2 stmts"}, Fix: "add tests"}},
				Total:  50, Ran: []string{"build", "tests", "coverage"},
			}
			h.touch("main.stub")
		}},
		{name: "stop_pass", event: hook.Stop, payload: "stop", setup: func(h *harness) {
			h.mod.gate = module.GateResult{Total: 95, Coverage: map[string]float64{"pkg": 95}, Ran: []string{"build"}}
			h.touch("main.stub")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			if tc.setup != nil {
				tc.setup(h)
			}
			h.golden(tc.name, h.handle(tc.event, h.payload(tc.payload)))
		})
	}
}

func (h *harness) touch(rel string) {
	h.t.Helper()
	_, err := h.eng.Store.Update("s1", func(s *state.Session) error {
		s.Repo = h.repo
		s.Touch("", filepath.Join(h.repo, rel))
		return nil
	})
	if err != nil {
		h.t.Fatal(err)
	}
}

func TestHandleErrors(t *testing.T) {
	h := newHarness(t)
	if _, err := h.eng.Handle(context.Background(), hook.Stop, hook.Input{}); err == nil {
		t.Error("missing session_id: want error")
	}
	if _, err := h.eng.Handle(context.Background(), "Bogus", hook.Input{SessionID: "s"}); err == nil {
		t.Error("unknown event: want error")
	}
	bad := hook.Input{SessionID: "s1", ToolName: "Edit", ToolInput: json.RawMessage(`"nope"`)}
	if _, err := h.eng.Handle(context.Background(), hook.PreToolUse, bad); err == nil {
		t.Error("bad tool_input in PreToolUse: want error")
	}
	if _, err := h.eng.Handle(context.Background(), hook.PostToolUse, bad); err == nil {
		t.Error("bad tool_input in PostToolUse: want error")
	}
}

func TestNormalizeEventAndBlocking(t *testing.T) {
	for in, want := range map[string]string{
		"session-start": hook.SessionStart, "PreToolUse": hook.PreToolUse,
		"post_tool_use": hook.PostToolUse, "stop": hook.Stop,
		"subagent-stop": hook.SubagentStop, "SESSIONEND": hook.SessionEnd,
	} {
		got, ok := NormalizeEvent(in)
		if !ok || got != want {
			t.Errorf("NormalizeEvent(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	if _, ok := NormalizeEvent("nope"); ok {
		t.Error("NormalizeEvent(nope) ok")
	}
	for ev, want := range map[string]bool{
		hook.PreToolUse: true, hook.PostToolUse: true, hook.Stop: true, hook.SubagentStop: true,
		hook.SessionStart: false, hook.SessionEnd: false,
	} {
		if Blocking(ev) != want {
			t.Errorf("Blocking(%s) != %v", ev, want)
		}
	}
}

func TestRepoRootFallsBackToCwd(t *testing.T) {
	e := New(state.New(t.TempDir()), runner.NewFake(), "")
	dir := t.TempDir()
	if got := e.RepoRoot(context.Background(), dir); got != dir {
		t.Errorf("RepoRoot = %q, want %q", got, dir)
	}
	wd, _ := os.Getwd()
	if got := e.RepoRoot(context.Background(), ""); got != wd {
		t.Errorf("RepoRoot(\"\") = %q, want %q", got, wd)
	}
	if len(e.Modules) != 1 || e.Modules[0].Name() != "go" {
		t.Errorf("default modules = %v", e.Modules)
	}
}

func TestSessionStart(t *testing.T) {
	t.Run("startup resets and resume keeps", func(t *testing.T) {
		h := newHarness(t)
		h.touch("main.stub")
		_, _ = h.eng.Store.Update("s1", func(s *state.Session) error { s.StopBlocks = 2; return nil })
		if err := h.eng.Store.SaveRepo(h.repo, &state.RepoCache{Coverage: map[string]float64{"p": 81}}); err != nil {
			t.Fatal(err)
		}
		h.handle(hook.SessionStart, h.in(hook.SessionStart, map[string]any{"source": "startup"}))
		s := h.session()
		if len(s.Touched) != 0 || s.StopBlocks != 0 || s.BaseCommit != "abc123" || s.Baseline["p"] != 81 {
			t.Fatalf("startup did not reset: %+v", s)
		}
		if !s.Started.Equal(fixedNow) {
			t.Errorf("Started = %v", s.Started)
		}
		h.touch("main.stub")
		h.handle(hook.SessionStart, h.in(hook.SessionStart, map[string]any{"source": "resume"}))
		if len(h.session().Touched) != 1 {
			t.Error("resume dropped touched files")
		}
	})
	t.Run("config error", func(t *testing.T) {
		h := newHarness(t)
		h.config("bogus: 1\n")
		out := h.handle(hook.SessionStart, h.in(hook.SessionStart, nil))
		if !strings.Contains(out.SystemMessage, "config error") || out.Specific == nil ||
			!strings.Contains(out.Specific.AdditionalContext, "owned by the human") {
			t.Errorf("out = %+v", out)
		}
	})
	t.Run("no active modules", func(t *testing.T) {
		h := newHarness(t)
		h.mod.enabled = false
		if out := h.handle(hook.SessionStart, h.in(hook.SessionStart, nil)); !out.IsZero() {
			t.Errorf("want empty output, got %+v", out)
		}
	})
	t.Run("module files absent", func(t *testing.T) {
		h := newHarness(t)
		_ = os.Remove(filepath.Join(h.repo, "main.stub"))
		h.write("a/b/c/deep.stub", "x")
		if out := h.handle(hook.SessionStart, h.in(hook.SessionStart, nil)); !out.IsZero() {
			t.Errorf("want empty output, got %+v", out)
		}
	})
	t.Run("nested module file detected", func(t *testing.T) {
		h := newHarness(t)
		_ = os.Remove(filepath.Join(h.repo, "main.stub"))
		h.write("sub/one.stub", "x")
		if out := h.handle(hook.SessionStart, h.in(hook.SessionStart, nil)); out.Specific == nil {
			t.Error("want context for nested module file")
		}
	})
	t.Run("baseline and last gate in summary", func(t *testing.T) {
		h := newHarness(t)
		_ = h.eng.Store.SaveRepo(h.repo, &state.RepoCache{
			Coverage: map[string]float64{"a": 80, "b": 90},
			LastGate: &state.GateSummary{Passed: false, Issues: 4},
		})
		out := h.handle(hook.SessionStart, h.in(hook.SessionStart, nil))
		ctx := out.Specific.AdditionalContext
		for _, want := range []string{"averages 85.0% across 2 packages", "Last gate failed with 4 lint issues"} {
			if !strings.Contains(ctx, want) {
				t.Errorf("context missing %q:\n%s", want, ctx)
			}
		}
	})
	t.Run("warn mode and go summary", func(t *testing.T) {
		cfg := config.Default()
		cfg.Mode = config.ModeWarn
		rc := &state.RepoCache{Coverage: map[string]float64{"x": 70}, LastGate: &state.GateSummary{Passed: true}}
		got := standardsSummary(cfg, []module.Module{goNamed{newStub()}}, rc)
		for _, want := range []string{"warn mode", "Go standards", "90% total", "none below its starting", "lines changed", "govulncheck", "Last gate passed"} {
			if !strings.Contains(got, want) {
				t.Errorf("summary missing %q:\n%s", want, got)
			}
		}
		cfg.Go.Coverage.Ratchet, cfg.Go.PatchCoverage, cfg.Go.Vuln = false, 0, false
		got = standardsSummary(cfg, []module.Module{goNamed{newStub()}}, &state.RepoCache{})
		for _, absent := range []string{"starting coverage", "lines changed", "govulncheck", "Baseline"} {
			if strings.Contains(got, absent) {
				t.Errorf("summary should not mention %q:\n%s", absent, got)
			}
		}
	})
}

// goNamed reports itself as the go module for summary rendering.
type goNamed struct{ *stub }

func (goNamed) Name() string { return "go" }

func TestPreToolUse(t *testing.T) {
	edit := func(h *harness, tool string, input map[string]any, extra map[string]any) hook.Output {
		f := map[string]any{"tool_name": tool, "tool_input": input}
		for k, v := range extra {
			f[k] = v
		}
		return h.handle(hook.PreToolUse, h.in(hook.PreToolUse, f))
	}
	t.Run("write allowed", func(t *testing.T) {
		h := newHarness(t)
		out := edit(h, "Write", map[string]any{"file_path": "new.go", "content": "package x\n"}, nil)
		if !out.IsZero() {
			t.Errorf("want allow, got %+v", out)
		}
	})
	t.Run("multiedit skipped test denied", func(t *testing.T) {
		h := newHarness(t)
		p := h.write("x_test.go", "package x\n\nfunc TestX(t *testing.T) {\n}\n")
		out := edit(h, "MultiEdit", map[string]any{"file_path": p, "edits": []map[string]string{
			{"old_string": "func TestX(t *testing.T) {\n", "new_string": "func TestX(t *testing.T) {\n\tt.Skip(\"later\")\n"},
		}}, nil)
		if !out.Blocking() || !strings.Contains(out.Specific.PermissionDecisionReason, "skipped-test") {
			t.Errorf("want deny, got %+v", out.Specific)
		}
	})
	t.Run("test-writer restricted to tests", func(t *testing.T) {
		h := newHarness(t)
		out := edit(h, "Write", map[string]any{"file_path": "calc.go", "content": "package calc\n"}, map[string]any{"agent_type": "test-writer"})
		if !out.Blocking() || !strings.Contains(out.Specific.PermissionDecisionReason, "test-writer") {
			t.Errorf("want deny, got %+v", out.Specific)
		}
		for _, f := range []string{"calc_test.go", "test_calc.py", "calc_test.py", "pkg/testdata/in.txt"} {
			if out := edit(h, "Write", map[string]any{"file_path": f, "content": "x"}, map[string]any{"agent_type": "test-writer"}); !out.IsZero() {
				t.Errorf("%s: want allow, got %+v", f, out.Specific)
			}
		}
	})
	t.Run("empty path and unreadable path", func(t *testing.T) {
		h := newHarness(t)
		if out := edit(h, "Edit", map[string]any{}, nil); !out.IsZero() {
			t.Errorf("empty path: got %+v", out)
		}
		dir := filepath.Join(h.repo, "adir")
		_ = os.Mkdir(dir, 0o755)
		_, err := h.eng.Handle(context.Background(), hook.PreToolUse, h.in(hook.PreToolUse, map[string]any{
			"tool_name": "Edit", "tool_input": map[string]any{"file_path": dir, "old_string": "a", "new_string": "b"},
		}))
		if err == nil {
			t.Error("reading a directory: want error")
		}
	})
	t.Run("other tool ignored", func(t *testing.T) {
		h := newHarness(t)
		if out := edit(h, "Read", map[string]any{"file_path": "x"}, nil); !out.IsZero() {
			t.Errorf("got %+v", out)
		}
	})
	t.Run("invalid config still guards", func(t *testing.T) {
		h := newHarness(t)
		h.config("nope: true\n")
		out := edit(h, "Bash", map[string]any{"command": "git push --force"}, nil)
		if !out.Blocking() {
			t.Errorf("want deny, got %+v", out)
		}
	})
	t.Run("bash uses current branch and kube context", func(t *testing.T) {
		h := newHarness(t)
		h.fake.On("git rev-parse --abbrev-ref HEAD", runner.Result{Stdout: "main\n"})
		if out := edit(h, "Bash", map[string]any{"command": "git push"}, nil); !out.Blocking() {
			t.Errorf("push to main: want deny, got %+v", out)
		}
		h.fake.On("kubectl config current-context", runner.Result{Stdout: "prod\n"})
		out := edit(h, "Bash", map[string]any{"command": "kubectl apply -f x.yaml"}, nil)
		if out.Specific == nil || out.Specific.PermissionDecision != hook.Ask {
			t.Errorf("kubectl prod: want ask, got %+v", out)
		}
		h.fake.On("kubectl config current-context", runner.Result{ExitCode: 1})
		out = edit(h, "Bash", map[string]any{"command": "kubectl apply -f x.yaml"}, nil)
		if out.Specific == nil || !strings.Contains(out.Specific.PermissionDecisionReason, "unknown") {
			t.Errorf("kubectl unknown: got %+v", out)
		}
	})
}

func TestPostToolUse(t *testing.T) {
	post := func(h *harness, path string, extra map[string]any) hook.Output {
		f := map[string]any{"tool_name": "Edit", "tool_input": map[string]any{"file_path": path}}
		for k, v := range extra {
			f[k] = v
		}
		return h.handle(hook.PostToolUse, h.in(hook.PostToolUse, f))
	}
	t.Run("ignored paths", func(t *testing.T) {
		h := newHarness(t)
		h.mod.checks = []feedback.Check{lintFail}
		for _, p := range []string{"", filepath.Join(t.TempDir(), "x.stub"), "readme.md"} {
			if out := post(h, p, nil); !out.IsZero() {
				t.Errorf("%q: got %+v", p, out)
			}
		}
		if h.mod.checkRuns != 0 || len(h.session().Touched) != 0 {
			t.Error("ignored paths should not be checked or touched")
		}
	})
	t.Run("config error", func(t *testing.T) {
		h := newHarness(t)
		h.config("mode: maybe\n")
		if out := post(h, "main.stub", nil); !strings.Contains(out.SystemMessage, "config error") {
			t.Errorf("got %+v", out)
		}
	})
	t.Run("fix notes and dedup", func(t *testing.T) {
		h := newHarness(t)
		h.mod.fixNotes = []string{"formatter missing"}
		h.mod.checks = []feedback.Check{lintFail}
		out := post(h, "main.stub", map[string]any{"agent_id": "a1"})
		if out.SystemMessage != "guardrails: formatter missing" || !strings.Contains(out.Reason, "bad thing") {
			t.Fatalf("first: %+v", out)
		}
		out = post(h, "main.stub", nil)
		if !out.Blocking() || strings.Contains(out.Reason, "bad thing") || !strings.Contains(out.Reason, "1 previously reported") {
			t.Errorf("second should dedup: %q", out.Reason)
		}
		s := h.session()
		if !s.Touched[filepath.Join(h.repo, "main.stub")] || len(s.AgentTouched["a1"]) != 1 || len(s.Reported) != 1 {
			t.Errorf("session = %+v", s)
		}
	})
	t.Run("warn mode", func(t *testing.T) {
		h := newHarness(t)
		h.config("mode: warn\n")
		h.mod.checks = []feedback.Check{lintFail}
		out := post(h, "main.stub", nil)
		if out.Blocking() || !strings.Contains(out.SystemMessage, "bad thing") {
			t.Errorf("got %+v", out)
		}
	})
	t.Run("clean result cached", func(t *testing.T) {
		h := newHarness(t)
		post(h, "main.stub", nil)
		post(h, "main.stub", nil)
		if h.mod.checkRuns != 1 {
			t.Errorf("checkRuns = %d, want 1", h.mod.checkRuns)
		}
		h.write("main.stub", "changed\n")
		post(h, "main.stub", nil)
		if h.mod.checkRuns != 2 {
			t.Errorf("checkRuns after change = %d, want 2", h.mod.checkRuns)
		}
	})
	t.Run("deleted file is still checked", func(t *testing.T) {
		h := newHarness(t)
		post(h, "gone.stub", nil)
		post(h, "gone.stub", nil)
		if h.mod.checkRuns != 2 {
			t.Errorf("checkRuns = %d, want 2 (no cache without content)", h.mod.checkRuns)
		}
	})
	t.Run("truncation writes overflow", func(t *testing.T) {
		h := newHarness(t)
		c := feedback.Check{Name: "lint", Failed: true, Summary: "many"}
		for i := 0; i < 30; i++ {
			c.Issues = append(c.Issues, feedback.Issue{File: "main.stub", Line: i + 1, Rule: "r", Message: "issue"})
		}
		h.mod.checks = []feedback.Check{c}
		out := post(h, "main.stub", nil)
		overflow := filepath.Join(h.eng.Store.SessionDir("s1"), "last-edit-report.txt")
		if !strings.Contains(out.Reason, "full report: ") {
			t.Errorf("reason missing pointer: %q", out.Reason)
		}
		data, err := os.ReadFile(overflow)
		if err != nil || strings.Count(string(data), "issue") != 30 {
			t.Errorf("overflow = %q, %v", data, err)
		}
	})
}

func TestStop(t *testing.T) {
	failing := module.GateResult{
		Checks: []feedback.Check{lintFail}, Total: 70, Ran: []string{"build", "lint"},
		Notes: []string{"vuln skipped"},
	}
	stop := func(h *harness) hook.Output { return h.handle(hook.Stop, h.in(hook.Stop, nil)) }

	t.Run("no touched files", func(t *testing.T) {
		h := newHarness(t)
		if out := stop(h); !out.IsZero() || h.mod.gateRuns != 0 {
			t.Errorf("got %+v, gateRuns %d", out, h.mod.gateRuns)
		}
	})
	t.Run("touched files no module owns", func(t *testing.T) {
		h := newHarness(t)
		h.touch("readme.md")
		if out := stop(h); !out.IsZero() {
			t.Errorf("got %+v", out)
		}
	})
	t.Run("config error", func(t *testing.T) {
		h := newHarness(t)
		h.touch("main.stub")
		h.config("version: 2\n")
		if out := stop(h); !strings.Contains(out.SystemMessage, "config error") {
			t.Errorf("got %+v", out)
		}
	})
	t.Run("blocks then hands off then resets", func(t *testing.T) {
		h := newHarness(t)
		h.touch("main.stub")
		h.mod.gate = failing
		for i := 1; i <= 3; i++ {
			out := stop(h)
			if !out.Blocking() || !strings.Contains(out.Reason, "attempt "+string(rune('0'+i))+" of 3") {
				t.Fatalf("attempt %d: %+v", i, out)
			}
			if !strings.Contains(out.Reason, "(1 of 2 checks)") || out.SystemMessage != "guardrails: vuln skipped" {
				t.Errorf("attempt %d: reason %q msg %q", i, out.Reason, out.SystemMessage)
			}
		}
		out := stop(h)
		if out.Blocking() || !strings.Contains(out.SystemMessage, "handing off to you") {
			t.Fatalf("4th: %+v", out)
		}
		s := h.session()
		if s.StopBlocks != 0 || s.HandedOff != 1 || s.Blocked != 3 || s.Gates != 4 {
			t.Errorf("session = %+v", s)
		}
		if out := stop(h); !strings.Contains(out.Reason, "attempt 1 of 3") {
			t.Errorf("after handoff the counter restarts: %+v", out)
		}
		rc, _ := h.eng.Store.LoadRepo(h.repo)
		if rc.LastGate == nil || rc.LastGate.Passed || rc.LastGate.Issues != 1 || len(rc.Coverage) != 0 {
			t.Errorf("repo cache = %+v", rc)
		}
		data, err := os.ReadFile(filepath.Join(h.data, runlog.FileName))
		if err != nil || strings.Count(string(data), `"type":"gate"`) != 5 {
			t.Errorf("runs.jsonl = %s, %v", data, err)
		}
	})
	t.Run("pass resets counter and raises baseline", func(t *testing.T) {
		h := newHarness(t)
		h.touch("main.stub")
		_, _ = h.eng.Store.Update("s1", func(s *state.Session) error {
			s.StopBlocks = 2
			s.Baseline = map[string]float64{"pkg": 80, "other": 99}
			s.BaseCommit = "base1"
			return nil
		})
		h.mod.gate = module.GateResult{Coverage: map[string]float64{"pkg": 92, "other": 95}, Total: 93}
		if out := stop(h); !out.IsZero() {
			t.Errorf("pass: got %+v", out)
		}
		s := h.session()
		if s.StopBlocks != 0 || s.Baseline["pkg"] != 92 || s.Baseline["other"] != 99 {
			t.Errorf("session = %+v", s)
		}
		if in := h.mod.gateIn[0]; in.BaseCommit != "base1" || in.Baseline["pkg"] != 80 || in.All {
			t.Errorf("gate input = %+v", in)
		}
		rc, _ := h.eng.Store.LoadRepo(h.repo)
		if rc.Coverage["pkg"] != 92 || !rc.LastGate.Passed || rc.LastGate.Coverage != 93 {
			t.Errorf("repo cache = %+v", rc)
		}
	})
	t.Run("pass with no prior baseline", func(t *testing.T) {
		h := newHarness(t)
		h.touch("main.stub")
		h.mod.gate = module.GateResult{Coverage: map[string]float64{"pkg": 91}, Total: 91}
		stop(h)
		if h.session().Baseline["pkg"] != 91 {
			t.Errorf("baseline = %v", h.session().Baseline)
		}
	})
	t.Run("warn mode", func(t *testing.T) {
		h := newHarness(t)
		h.config("mode: warn\n")
		h.touch("main.stub")
		h.mod.gate = failing
		out := stop(h)
		if out.Blocking() || !strings.Contains(out.SystemMessage, "bad thing") || !strings.Contains(out.SystemMessage, "vuln skipped") {
			t.Errorf("got %+v", out)
		}
	})
	t.Run("repo from cwd when session has none", func(t *testing.T) {
		h := newHarness(t)
		_, _ = h.eng.Store.Update("s1", func(s *state.Session) error {
			s.Touch("", filepath.Join(h.repo, "main.stub"))
			return nil
		})
		stop(h)
		if h.mod.gateRuns != 1 {
			t.Errorf("gateRuns = %d", h.mod.gateRuns)
		}
	})
	t.Run("timeout adds a failing check", func(t *testing.T) {
		h := newHarness(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		g := h.eng.Gate(ctx, h.repo, config.Default(), []string{filepath.Join(h.repo, "main.stub")}, "", nil, false)
		if len(g.Checks) != 1 || g.Checks[0].Name != "timeout" {
			t.Errorf("checks = %+v", g.Checks)
		}
	})
	t.Run("gate all includes modules without files", func(t *testing.T) {
		h := newHarness(t)
		h.mod.gate = module.GateResult{Total: -1}
		g := h.eng.Gate(context.Background(), h.repo, config.Default(), nil, "", nil, true)
		if len(g.Modules) != 1 || !h.mod.gateIn[0].All || g.Total != -1 {
			t.Errorf("outcome = %+v", g)
		}
		h.mod.enabled = false
		if g := h.eng.Gate(context.Background(), h.repo, config.Default(), nil, "", nil, true); len(g.Modules) != 0 {
			t.Errorf("disabled module ran: %+v", g)
		}
	})
}

func TestSubagentStopScopedToAgent(t *testing.T) {
	h := newHarness(t)
	_, _ = h.eng.Store.Update("s1", func(s *state.Session) error {
		s.Repo = h.repo
		s.Touch("", filepath.Join(h.repo, "main.stub"))
		s.Touch("agent-1", filepath.Join(h.repo, "agent.stub"))
		return nil
	})
	h.mod.gate = module.GateResult{Checks: []feedback.Check{lintFail}, Total: -1}
	out := h.handle(hook.SubagentStop, h.in(hook.SubagentStop, map[string]any{"agent_id": "agent-1", "agent_type": "implementer"}))
	if !out.Blocking() {
		t.Fatalf("want block, got %+v", out)
	}
	if got := h.mod.gateFiles[0]; len(got) != 1 || filepath.Base(got[0]) != "agent.stub" {
		t.Errorf("gate files = %v", got)
	}
	s := h.session()
	if s.AgentBlocks["agent-1"] != 1 || s.StopBlocks != 0 {
		t.Errorf("counters: agent %v main %d", s.AgentBlocks, s.StopBlocks)
	}
	// An agent identified only by type uses the type as its key.
	if out := h.handle(hook.SubagentStop, h.in(hook.SubagentStop, map[string]any{"agent_type": "test-writer"})); !out.IsZero() {
		t.Errorf("agent with no files: %+v", out)
	}
}

func TestSessionEnd(t *testing.T) {
	h := newHarness(t)
	h.touch("main.stub")
	_, _ = h.eng.Store.Update("s1", func(s *state.Session) error {
		s.Started = fixedNow.Add(-time.Minute)
		s.Gates, s.Blocked, s.HandedOff = 3, 2, 1
		return nil
	})
	if out := h.handle(hook.SessionEnd, h.in(hook.SessionEnd, map[string]any{"reason": "prompt_input_exit"})); !out.IsZero() {
		t.Errorf("got %+v", out)
	}
	data, err := os.ReadFile(filepath.Join(h.data, runlog.FileName))
	if err != nil {
		t.Fatal(err)
	}
	var rec runlog.Record
	if err := json.Unmarshal(bytes.TrimSpace(data), &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Type != "session" || rec.Reason != "prompt_input_exit" || rec.Gates != 3 || rec.Blocked != 2 ||
		!rec.HandedOff || rec.Touched != 1 || rec.Millis != 60000 || rec.Repo != h.repo {
		t.Errorf("record = %+v", rec)
	}
}

func TestStateErrorsSurface(t *testing.T) {
	h := newHarness(t)
	dir := h.eng.Store.SessionDir("s1")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte("{corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, ev := range []string{hook.SessionStart, hook.Stop, hook.SessionEnd} {
		if _, err := h.eng.Handle(context.Background(), ev, h.in(ev, nil)); err == nil {
			t.Errorf("%s: want error for corrupt state", ev)
		}
	}
	in := h.in(hook.PostToolUse, map[string]any{"tool_name": "Edit", "tool_input": map[string]any{"file_path": "main.stub"}})
	if _, err := h.eng.Handle(context.Background(), hook.PostToolUse, in); err == nil {
		t.Error("PostToolUse: want error for corrupt state")
	}
}

func TestStopUpdateError(t *testing.T) {
	h := newHarness(t)
	h.touch("main.stub")
	h.mod.gate = module.GateResult{Total: -1}
	// Hold the session lock so Update times out.
	h.eng.Store.LockTimeout = 20 * time.Millisecond
	lockPath := filepath.Join(h.eng.Store.SessionDir("s1"), "state.lock")
	if err := os.WriteFile(lockPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.eng.Handle(context.Background(), hook.Stop, h.in(hook.Stop, nil)); err == nil {
		t.Error("want lock timeout error")
	}
}

func TestRecordGateCorruptCache(t *testing.T) {
	h := newHarness(t)
	p := filepath.Join(h.data, "repos", state.Key(h.repo)+".json")
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	_ = os.WriteFile(p, []byte("{bad"), 0o600)
	h.eng.RecordGate(h.repo, "/r.txt", true, 91, map[string]float64{"p": 91}, config.Default(), nil)
	rc, err := h.eng.Store.LoadRepo(h.repo)
	if err != nil || rc.Coverage["p"] != 91 || rc.LastReport != "/r.txt" {
		t.Errorf("rc = %+v, %v", rc, err)
	}
}

func TestHelpers(t *testing.T) {
	checks := []feedback.Check{
		{Name: "build", Failed: true},
		{Name: "vet", Failed: true, Issues: []feedback.Issue{{File: "a.go", Rule: "vet"}}},
		{Name: "lint", Failed: true, Issues: []feedback.Issue{{File: "b.go", Rule: "errcheck"}, {File: "c.go"}}},
		{Name: "ok", Failed: false},
	}
	fs := failures(checks)
	if len(fs) != 4 || fs[0].Check != "build" || fs[2].Rule != "errcheck" {
		t.Errorf("failures = %+v", fs)
	}
	if n := countIssues(checks); n != 3 {
		t.Errorf("countIssues = %d", n)
	}
	if notes(nil) != "" {
		t.Error("notes(nil) not empty")
	}
	if copyMap(nil) != nil {
		t.Error("copyMap(nil) not nil")
	}
	if within("/a/b", "/a/c") || !within("/a/b", "/a/b/c") {
		t.Error("within wrong")
	}
	if absPath("/x", "y") != "/x/y" || absPath("/x", "/z") != "/z" || absPath("/x", "") != "" {
		t.Error("absPath wrong")
	}
	if displayRel("/a", "/a/b/c.go") != "b/c.go" {
		t.Error("displayRel wrong")
	}
	if got := displayRel("/a", "rel"); got != "rel" {
		t.Errorf("displayRel(rel) = %q", got)
	}
	if err := writeFile(filepath.Join(t.TempDir(), "f", "x"), "x"); err != nil {
		t.Error(err)
	}
	blocker := filepath.Join(t.TempDir(), "file")
	_ = os.WriteFile(blocker, nil, 0o600)
	if err := writeFile(filepath.Join(blocker, "x", "y"), "x"); err == nil {
		t.Error("writeFile under a file: want error")
	}
	if agentKey(hook.Input{AgentType: "t"}) != "t" || agentKey(hook.Input{AgentID: "i", AgentType: "t"}) != "i" {
		t.Error("agentKey wrong")
	}
}
