package testdepth

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rossedman/guardrails/internal/coverage"
	"github.com/rossedman/guardrails/internal/diff"
	"github.com/rossedman/guardrails/internal/runner"
)

// Changes returns the Go lines changed against base in repo, keyed by
// absolute path, plus every changed or untracked .go file (tests included)
// as touched. Untracked files count as entirely changed.
func Changes(ctx context.Context, r runner.Runner, repo, base string) (map[string]coverage.LineSet, []string, error) {
	res, err := r.Run(ctx, repo, "git", "diff", "--unified=0", "--no-color", "--no-ext-diff",
		"--src-prefix=a/", "--dst-prefix=b/", base, "--", "*.go")
	if err != nil {
		return nil, nil, err
	}
	if res.ExitCode != 0 {
		return nil, nil, fmt.Errorf("git diff %s: %s", base, strings.TrimSpace(res.Combined()))
	}
	changed := map[string]coverage.LineSet{}
	for rel, set := range diff.ParseUnified(res.Stdout) {
		changed[filepath.Join(repo, filepath.FromSlash(rel))] = set
	}
	untracked, err := r.Run(ctx, repo, "git", "ls-files", "--others", "--exclude-standard", "--", "*.go")
	if err == nil && untracked.ExitCode == 0 {
		for _, rel := range strings.Fields(untracked.Stdout) {
			abs := filepath.Join(repo, filepath.FromSlash(rel))
			changed[abs] = allLines(abs)
		}
	}
	touched := make([]string, 0, len(changed))
	for p := range changed {
		touched = append(touched, p)
	}
	return changed, touched, nil
}

func allLines(path string) coverage.LineSet {
	set := coverage.LineSet{}
	data, err := os.ReadFile(path)
	if err != nil {
		return set
	}
	for i := range strings.Split(string(data), "\n") {
		set[i+1] = true
	}
	return set
}
