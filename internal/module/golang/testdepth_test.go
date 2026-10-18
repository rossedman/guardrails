package golang

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rossedman/guardrails/internal/config"
	"github.com/rossedman/guardrails/internal/module"
	"github.com/rossedman/guardrails/internal/runner"
)

// quietDepth turns off every test-depth check that could do work, so these
// tests exercise only the gate's plumbing.
func quietDepth(env *module.Env) {
	env.Config.Go.Mutation.Tool = config.MutationOff
	env.Config.Go.Bench.RequireOnHotPaths = false
	env.Config.Go.Fuzz.RequireOnParsers = false
}

// gitDiffs counts `git diff` calls: patch coverage takes one, test depth
// another.
func gitDiffs(f *runner.Fake) int {
	n := 0
	for _, c := range f.Calls() {
		if c.Name == "git" && len(c.Args) > 0 && c.Args[0] == "diff" {
			n++
		}
	}
	return n
}

const depthDiff = "+++ b/a/a.go\n@@ -1,0 +8,1 @@\n+x\n"

func TestGateTestDepthRuns(t *testing.T) {
	t.Parallel()
	repo := writeRepo(t, map[string]string{"a/a.go": aSource})
	f := fakeAll().
		OnFunc("go test", profileWriter(aProfileFull)).
		On("git diff", runner.Result{Stdout: depthDiff})
	env := newEnv(t, repo, f)
	quietDepth(&env)
	res := New().Gate(context.Background(), env, module.GateInput{
		Files:      []string{filepath.Join(repo, "a/a.go")},
		BaseCommit: "abc",
		Asked:      map[string]bool{"x": true},
	})
	if len(res.Checks) != 0 {
		t.Fatalf("want a passing gate, got %v", checkNames(res.Checks))
	}
	if got := gitDiffs(f); got != 2 {
		t.Errorf("git diff calls = %d, want 2 (patch coverage + test depth)", got)
	}
	if len(res.Asked) != 0 {
		t.Errorf("quiet depth raised Asked = %v", res.Asked)
	}
	for _, c := range f.Calls() {
		if slices.ContainsFunc(c.Args, func(a string) bool { return strings.HasPrefix(a, "-overlay") }) {
			t.Errorf("mutation is off but ran %s", c)
		}
	}
}

func TestGateTestDepthSkipped(t *testing.T) {
	t.Parallel()
	repo := writeRepo(t, map[string]string{"a/a.go": aSource})
	file := filepath.Join(repo, "a/a.go")
	tests := []struct {
		name    string
		profile string
		in      module.GateInput
	}{
		// Low coverage fails the gate first; depth is not worth running.
		{"failing checks", aProfileLow, module.GateInput{Files: []string{file}, BaseCommit: "abc"}},
		{"no base commit", aProfileFull, module.GateInput{Files: []string{file}}},
		{"all mode", aProfileFull, module.GateInput{Files: []string{file}, BaseCommit: "abc", All: true}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := fakeAll().
				OnFunc("go test", profileWriter(tc.profile)).
				On("git diff", runner.Result{Stdout: depthDiff})
			env := newEnv(t, repo, f)
			quietDepth(&env)
			env.Config.Go.PatchCoverage = 0
			New().Gate(context.Background(), env, tc.in)
			if got := gitDiffs(f); got != 0 {
				t.Errorf("git diff calls = %d, want none (patch coverage off, depth skipped)", got)
			}
		})
	}
}

func TestGateTestDepthParseErrorNote(t *testing.T) {
	t.Parallel()
	// The fake build succeeds, but the changed file can't be parsed by the
	// analysis: the gate notes that test depth was skipped.
	broken := aSource + "\nfunc Broken( {\n"
	repo := writeRepo(t, map[string]string{"a/a.go": broken})
	f := fakeAll().
		OnFunc("go test", profileWriter(aProfileFull)).
		On("git diff", runner.Result{Stdout: depthDiff})
	env := newEnv(t, repo, f)
	quietDepth(&env)
	res := New().Gate(context.Background(), env, module.GateInput{
		Files:      []string{filepath.Join(repo, "a/a.go")},
		BaseCommit: "abc",
	})
	if len(res.Checks) != 0 {
		t.Fatalf("checks = %v", checkNames(res.Checks))
	}
	if !slices.ContainsFunc(res.Notes, func(n string) bool { return strings.HasPrefix(n, "test depth skipped: ") }) {
		t.Errorf("notes = %q", res.Notes)
	}
}
