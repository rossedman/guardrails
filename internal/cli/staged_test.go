package cli

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rossedman/guardrails/internal/module"
	"github.com/rossedman/guardrails/internal/runner"
)

func TestGateStaged(t *testing.T) {
	t.Run("gates only staged files against HEAD", func(t *testing.T) {
		f := newFixture(t)
		f.fake.On("git diff --cached --name-only --diff-filter=d", runner.Result{Stdout: "a.stub\nsub/b.stub\nnotes.md\n"})
		// Unstaged and untracked files must not be gated.
		f.fake.On("git diff --name-only", runner.Result{Stdout: "unstaged.stub\n"})
		f.fake.On("git ls-files --others", runner.Result{Stdout: "untracked.stub\n"})
		f.mod.gate = module.GateResult{Total: -1, Ran: []string{"build"}}

		if code := f.run("", "gate", "--staged", "--base", "main"); code != 0 {
			t.Fatalf("code = %d, stderr %q", code, f.stderr)
		}
		in := f.mod.in[0]
		want := []string{filepath.Join(f.repo, "a.stub"), filepath.Join(f.repo, "sub", "b.stub")}
		if len(in.Files) != 2 || in.Files[0] != want[0] || in.Files[1] != want[1] {
			t.Errorf("files = %v, want %v", in.Files, want)
		}
		if in.BaseCommit != "HEAD" || in.All {
			t.Errorf("--staged must measure patch coverage against HEAD: %+v", in)
		}
		if f.fake.Ran("git ls-files --others") {
			t.Error("--staged must not gate untracked files")
		}
	})

	t.Run("nothing staged", func(t *testing.T) {
		f := newFixture(t)
		f.fake.On("git diff --cached", runner.Result{})
		if code := f.run("", "gate", "--staged"); code != 0 || !strings.Contains(f.stdout.String(), "no staged changes") {
			t.Errorf("%d %q", code, f.stdout)
		}
		if len(f.mod.in) != 0 {
			t.Error("gate should not run")
		}
	})

	t.Run("git failures mean nothing staged", func(t *testing.T) {
		f := newFixture(t)
		f.fake.On("git diff --cached", runner.Result{ExitCode: 128, Stderr: "not a git repository"})
		if code := f.run("", "gate", "--staged"); code != 0 || !strings.Contains(f.stdout.String(), "no staged changes") {
			t.Errorf("exit: %d %q", code, f.stdout)
		}
		f = newFixture(t)
		f.fake.OnError("git diff --cached", errors.New("git missing"))
		if code := f.run("", "gate", "--staged"); code != 0 || !strings.Contains(f.stdout.String(), "no staged changes") {
			t.Errorf("error: %d %q", code, f.stdout)
		}
	})

	t.Run("staged and all are exclusive", func(t *testing.T) {
		f := newFixture(t)
		if code := f.run("", "gate", "--staged", "--all"); code != 2 || !strings.Contains(f.stderr.String(), "mutually exclusive") {
			t.Errorf("%d %q", code, f.stderr)
		}
	})
}
