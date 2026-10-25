package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rossedman/guardrails/internal/config"
	"github.com/rossedman/guardrails/internal/runner"
)

func TestHelpIsPlainWithoutATerminal(t *testing.T) {
	f := newFixture(t)
	if code := f.run("", "gate", "--help"); code != 0 {
		t.Fatalf("gate --help: %d", code)
	}
	if strings.Contains(f.stdout.String(), "\x1b[") {
		t.Errorf("a buffer is not a terminal, but help is colored: %q", f.stdout)
	}
	f.run("", "gat")
	if strings.Contains(f.stderr.String(), "\x1b[") {
		t.Errorf("a buffer is not a terminal, but the error is colored: %q", f.stderr)
	}
}

func TestHelpLayout(t *testing.T) {
	f := newFixture(t)
	f.run("", "help")
	out := f.stdout.String()
	for _, want := range []string{"DAILY", "status ", "learn ", "SETUP", "init ", "CLAUDE CODE", "hook <event>", "--data-dir", "# Set a repo up"} {
		if !strings.Contains(out, want) {
			t.Errorf("help lacks %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "DAILY") > strings.Index(out, "SETUP") || strings.Index(out, "SETUP") > strings.Index(out, "CLAUDE CODE") {
		t.Errorf("groups out of order:\n%s", out)
	}
	for _, hidden := range []string{"explain ", "insights ", "lessons "} {
		if strings.Contains(out, hidden) {
			t.Errorf("help lists the old command %q:\n%s", hidden, out)
		}
	}
	f.run("", "help", "tools", "path")
	if !strings.Contains(f.stdout.String(), "guardrails tools path <tool>") || !strings.Contains(f.stdout.String(), "--data-dir") {
		t.Errorf("tools path help: %q", f.stdout)
	}
}

func TestUnknownCommandSuggests(t *testing.T) {
	f := newFixture(t)
	if code := f.run("", "gat"); code != 2 || !strings.Contains(f.stderr.String(), "Did you mean this?\n\tgate") {
		t.Errorf("gat: %d %q", code, f.stderr)
	}
	if !strings.Contains(f.stderr.String(), "Run 'guardrails --help' for usage.") {
		t.Errorf("no usage hint: %q", f.stderr)
	}
	if code := f.run("", "gate", "extra"); code != 2 || !strings.Contains(f.stderr.String(), "Run 'guardrails gate --help'") {
		t.Errorf("gate extra: %d %q", code, f.stderr)
	}
}

func TestExitCodeError(t *testing.T) {
	if got := exitCode(3).Error(); got != "exit status 3" {
		t.Errorf("Error() = %q", got)
	}
}

func TestGlobalFlagsAndEnvironment(t *testing.T) {
	f := newFixture(t)
	elsewhere := t.TempDir()
	if code := f.run("", "--data-dir", elsewhere, "doctor"); !strings.Contains(f.stdout.String(), "data dir: "+elsewhere) {
		t.Errorf("--data-dir: %d %q", code, f.stdout)
	}
	t.Setenv("GUARDRAILS_DATA_DIR", elsewhere)
	f.run("", "doctor")
	if !strings.Contains(f.stdout.String(), "data dir: "+elsewhere) {
		t.Errorf("GUARDRAILS_DATA_DIR: %q", f.stdout)
	}

	user := filepath.Join(t.TempDir(), "team.yaml")
	if err := os.WriteFile(user, []byte("mode: warn\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := f.run("", "config", "--config", user); code != 0 || !strings.Contains(f.stdout.String(), "mode: warn") {
		t.Errorf("--config: %d %q %q", code, f.stdout, f.stderr)
	}
	t.Setenv("GUARDRAILS_CONFIG", user)
	if code := f.run("", "config"); code != 0 || !strings.Contains(f.stdout.String(), "mode: warn") {
		t.Errorf("GUARDRAILS_CONFIG: %d %q", code, f.stdout)
	}
}

func TestCommandFlagsFromEnvironment(t *testing.T) {
	f := newFixture(t)
	t.Setenv("GUARDRAILS_GATE_STAGED", "true")
	t.Setenv("GUARDRAILS_GATE_ALL", "1")
	if code := f.run("", "gate"); code != 2 || !strings.Contains(f.stderr.String(), "mutually exclusive") {
		t.Errorf("env flags: %d %q", code, f.stderr)
	}
	// A flag on the command line wins over the environment.
	f.fake.On("git diff --cached --name-only --diff-filter=d", runner.Result{})
	if code := f.run("", "gate", "--all=false"); code != 0 || !strings.Contains(f.stdout.String(), "no staged changes") {
		t.Errorf("flag over env: %d %q %q", code, f.stdout, f.stderr)
	}
}

func TestStateFallbacks(t *testing.T) {
	a := &App{Getenv: func(string) string { return "" }}
	if got := a.DataDir(); got != filepath.Join(os.TempDir(), "guardrails") {
		t.Errorf("DataDir() = %q", got)
	}
	if got := a.globalConfig(); got != "" {
		t.Errorf("globalConfig() = %q", got)
	}
	env := map[string]string{"XDG_DATA_HOME": "/x/data", "XDG_CONFIG_HOME": "/x/config"}
	a.Getenv = func(k string) string { return env[k] }
	if a.DataDir() != "/x/data/guardrails" || a.globalConfig() != "/x/config/guardrails/config.yaml" {
		t.Errorf("XDG: %q %q", a.DataDir(), a.globalConfig())
	}
}

func TestPrintYAMLError(t *testing.T) {
	if err := printYAML(failWriter{}, config.Default()); err == nil {
		t.Error("printYAML to a failing writer should fail")
	}
	if err := printYAML(io.Discard, config.Default()); err != nil {
		t.Error(err)
	}
}

func TestCompletion(t *testing.T) {
	f := newFixture(t)
	if code := f.run("", "__complete", "hook", ""); code != 0 || !strings.Contains(f.stdout.String(), "SubagentStop") {
		t.Errorf("hook completion: %d %q", code, f.stdout)
	}
	if code := f.run("", "__complete", "tools", "install", ""); code != 0 || strings.Contains(f.stdout.String(), "good") {
		t.Errorf("no manager: %d %q", code, f.stdout)
	}
	withToolManager(t, f)
	if code := f.run("", "__complete", "tools", "install", ""); code != 0 || !strings.Contains(f.stdout.String(), "good") {
		t.Errorf("tools completion: %d %q", code, f.stdout)
	}
	if code := f.run("", "completion", "zsh"); code != 0 || !strings.Contains(f.stdout.String(), "#compdef guardrails") {
		t.Errorf("completion zsh: %d", code)
	}
}

func TestToolsPathWithoutProvisioning(t *testing.T) {
	f := newFixture(t)
	if code := f.run("", "tools", "path", "ruff"); code != 1 || !strings.Contains(f.stderr.String(), "unavailable") {
		t.Errorf("tools path: %d %q", code, f.stderr)
	}
	if code := f.run("", "tools", "install"); code != 1 || !strings.Contains(f.stderr.String(), "unavailable") {
		t.Errorf("tools install: %d %q", code, f.stderr)
	}
}

func TestErrorsAreColoredInATerminal(t *testing.T) {
	t.Setenv("CLICOLOR_FORCE", "1")
	f := newFixture(t)
	if code := f.run("", "gate", "extra"); code != 2 {
		t.Fatalf("gate extra: %d", code)
	}
	if out := f.stderr.String(); !strings.Contains(out, "\x1b[") || !strings.Contains(out, "gate --help") {
		t.Errorf("forced color, but the error is plain: %q", out)
	}
}

func TestCommandBuildsTheTree(t *testing.T) {
	t.Parallel()
	root := New().Command()
	if root.Name() != "guardrails" {
		t.Errorf("root = %q", root.Name())
	}
	if c, _, err := root.Find([]string{"tools", "path"}); err != nil || c.Name() != "path" {
		t.Errorf("tools path = %v, %v", c, err)
	}
}
