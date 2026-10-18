package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rossedman/guardrails/internal/config"
	"github.com/rossedman/guardrails/internal/runner"
)

func TestHelpIsColoredOnlyInATerminal(t *testing.T) {
	f := newFixture(t)
	orig := isTerminal
	t.Cleanup(func() { isTerminal = orig })

	isTerminal = func(io.Writer) bool { return true }
	if code := f.run("", "gate", "--help"); code != 0 {
		t.Fatalf("gate --help: %d", code)
	}
	out := f.stdout.String()
	for _, want := range []string{styleHeading + "Usage:", styleName + "      --base", styleCode + "guardrails gate --base main"} {
		if !strings.Contains(out, want) {
			t.Errorf("colored help lacks %q:\n%s", want, out)
		}
	}
	f.run("", "--help")
	if !strings.Contains(f.stdout.String(), styleName+"gate") || !strings.Contains(f.stdout.String(), styleDim+"Run \"guardrails [command] --help\"") || !strings.Contains(f.stdout.String(), styleDim+"# Set a repo up") {
		t.Errorf("root help: %q", f.stdout)
	}

	f.env["NO_COLOR"] = "1"
	f.run("", "gate", "--help")
	if strings.Contains(f.stdout.String(), "\x1b[") {
		t.Errorf("NO_COLOR still colored: %q", f.stdout)
	}
	delete(f.env, "NO_COLOR")
	isTerminal = orig
	f.run("", "gate", "--help")
	if strings.Contains(f.stdout.String(), "\x1b[") {
		t.Errorf("a buffer is not a terminal: %q", f.stdout)
	}
	if isTerminal(os.Stdout) && isTerminal(&bytes.Buffer{}) {
		t.Error("a buffer is a terminal")
	}
}

func TestHelpLayout(t *testing.T) {
	f := newFixture(t)
	f.run("", "help")
	out := f.stdout.String()
	for _, want := range []string{"Claude Code:\n  hook", "Gates:\n  gate", "Setup:\n  init", "More:", "--data-dir"} {
		if !strings.Contains(out, want) {
			t.Errorf("help lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "guardrails [flags]") {
		t.Errorf("a command with subcommands shows a bare use line:\n%s", out)
	}
	f.run("", "help", "tools", "path")
	if !strings.Contains(f.stdout.String(), "guardrails tools path <tool>") || !strings.Contains(f.stdout.String(), "Global flags:") {
		t.Errorf("tools path help: %q", f.stdout)
	}

	var buf bytes.Buffer
	a := f.app
	a.V = newViper()
	root := NewRootCmd(a)
	if err := a.renderUsage(&buf, root); err != nil || !strings.Contains(buf.String(), "Usage:") || strings.Contains(buf.String(), "Quality gates") {
		t.Errorf("usage = %q, %v", buf.String(), err)
	}
	root.SetOut(&buf)
	buf.Reset()
	if err := root.Usage(); err != nil || !strings.Contains(buf.String(), "Usage:") {
		t.Errorf("Usage() = %q, %v", buf.String(), err)
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
