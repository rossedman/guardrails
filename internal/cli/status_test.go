package cli

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rossedman/guardrails/internal/config"
	"github.com/rossedman/guardrails/internal/lessons"
	"github.com/rossedman/guardrails/internal/module"
	"github.com/rossedman/guardrails/internal/runner"
	"github.com/rossedman/guardrails/internal/state"
)

func (f *fixture) status() (string, statusReport) {
	f.t.Helper()
	if code := f.run("", "status"); code != 0 {
		f.t.Fatalf("status: %d %q", code, f.stderr)
	}
	text := f.stdout.String()
	if code := f.run("", "status", "--json"); code != 0 {
		f.t.Fatalf("status --json: %d %q", code, f.stderr)
	}
	var rep statusReport
	if err := json.Unmarshal(f.stdout.Bytes(), &rep); err != nil {
		f.t.Fatalf("status --json: %v\n%s", err, f.stdout)
	}
	return text, rep
}

func (f *fixture) lastGate(passed bool) {
	f.t.Helper()
	rc := &state.RepoCache{
		LastGate:   &state.GateSummary{Passed: passed, Coverage: 91.5, Threshold: 90, Issues: 2, At: f.app.Now().Add(-3 * time.Hour)},
		LastReport: "/tmp/report.txt",
	}
	if err := state.New(f.data).SaveRepo(f.repo, rc); err != nil {
		f.t.Fatal(err)
	}
}

func TestStatusNewRepo(t *testing.T) {
	f := newFixture(t)
	f.env["GUARDRAILS_LAUNCHER"] = "/plugins/guardrails/bin/guardrails"
	text, rep := f.status()
	for _, want := range []string{
		"command   /plugins/guardrails/bin/guardrails",
		"ln -s /plugins/guardrails/bin/guardrails ~/.local/bin/guardrails",
		"○ defaults: strict: 90% coverage, 90% of changed lines (no .guardrails.yaml)",
		"enforce: failures block",
		"○ none found here",
		"○ none yet",
		"0 correction(s)",
		"deps      on:",
		"Next: run /guardrails:setup",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("status lacks %q:\n%s", want, text)
		}
	}
	if rep.OnPath || rep.Config.Preset != "strict" || len(rep.Config.Files) != 0 || rep.LastGate != nil || len(rep.Languages) != 0 {
		t.Errorf("json: %+v", rep)
	}
}

func TestStatusNextSteps(t *testing.T) {
	f := newFixture(t)
	fake := runner.NewFake("guardrails")
	fake.On("git rev-parse --show-toplevel", runner.Result{Stdout: f.repo + "\n"})
	f.app.Runner = fake
	f.write("main.stub", "x")
	f.write(config.RepoFile, "version: 1\npreset: adopt\nmode: warn\ndeps:\n  enabled: false\n")

	// The last gate failed: look at why.
	f.lastGate(false)
	text, rep := f.status()
	for _, want := range []string{
		"✓ adopt: coverage may not drop", config.RepoFile, "warn: failures are reported", "✓ Stub", "lint and format",
		"✗ failed 3h ago · coverage 91.5% (bar 90%) · 2 lint issue(s)", "report: /tmp/report.txt", "deps      off", "Next: see why the last gate failed: guardrails gate --explain",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("failed gate: status lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "ln -s") || !rep.OnPath || rep.Config.Preset != "adopt" || rep.Deps || rep.LastGate.Passed {
		t.Errorf("json: %+v", rep)
	}

	// Passed, with corrections waiting.
	f.lastGate(true)
	if err := lessons.Record(f.data, lessons.Lesson{Time: f.app.Now(), Session: "s", Repo: f.repo, Prompt: "no, don't do that"}); err != nil {
		t.Fatal(err)
	}
	if text, _ := f.status(); !strings.Contains(text, "✓ passed 3h ago") || !strings.Contains(text, "Next: turn 1 recorded correction(s) into rules: /guardrails:learn") {
		t.Errorf("lessons: %s", text)
	}

	// A required tool is missing.
	if _, err := lessons.Clear(f.data, f.repo, nil); err != nil {
		t.Fatal(err)
	}
	f.mod.tools = []module.Tool{{Name: "stub-lint", Required: true}, {Name: "stub-extra"}}
	if text, rep := f.status(); !strings.Contains(text, "✗ Stub") || !strings.Contains(text, "missing stub-lint") || strings.Contains(text, "stub-extra") ||
		!strings.Contains(text, "Next: install the missing tools") || len(rep.Languages[0].MissingTools) != 1 {
		t.Errorf("missing tool: %s", text)
	}

	// Nothing left.
	f.mod.tools = nil
	if text, _ := f.status(); !strings.Contains(text, "Next: nothing to do") {
		t.Errorf("clean: %s", text)
	}

	// No coverage figure for the last gate.
	rc := &state.RepoCache{LastGate: &state.GateSummary{Passed: true, Coverage: -1, At: f.app.Now()}}
	if err := state.New(f.data).SaveRepo(f.repo, rc); err != nil {
		t.Fatal(err)
	}
	if text, _ := f.status(); !strings.Contains(text, "passed just now · coverage n/a") || strings.Contains(text, "report:") {
		t.Errorf("no coverage: %s", text)
	}
}

func TestStatusProvisionedTools(t *testing.T) {
	f := newFixture(t)
	withToolManager(t, f)
	f.write("main.stub", "x")
	// "good" can be installed on demand, so it isn't missing.
	f.mod.tools = []module.Tool{{Name: "good", Required: true}}
	if text, _ := f.status(); strings.Contains(text, "missing good") {
		t.Errorf("auto-installed tool reported missing: %s", text)
	}
}

func TestStatusConfigError(t *testing.T) {
	f := newFixture(t)
	f.write(config.RepoFile, "bogus: true\n")
	text, rep := f.status()
	if !strings.Contains(text, "config    ✗") || !strings.Contains(text, "Next: fix the config error") || rep.Config.Error == "" {
		t.Errorf("config error: %s", text)
	}
	f.app.Getwd = func() (string, error) { return "", errors.New("gone") }
	if code := f.run("", "status"); code != 1 {
		t.Errorf("getwd: %d", code)
	}
}

func TestAgo(t *testing.T) {
	for d, want := range map[time.Duration]string{
		10 * time.Second: "just now",
		5 * time.Minute:  "5m ago",
		30 * time.Hour:   "30h ago",
		72 * time.Hour:   "3d ago",
	} {
		if got := ago(d); got != want {
			t.Errorf("ago(%v) = %q, want %q", d, got, want)
		}
	}
}
