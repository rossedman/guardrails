package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rossedman/guardrails/internal/config"
	"github.com/rossedman/guardrails/internal/feedback"
	"github.com/rossedman/guardrails/internal/state"
)

func newGateCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gate",
		Short: "Run the full quality gate on changed files (or --all)",
		Long: `Run the gate Claude meets when it stops: every enabled language module
builds, tests, measures coverage and lints the files changed against --base
(plus untracked files), or every package with --all.

It exits 1 when a check fails and prints the same short report Claude sees;
--explain prints all of it. The report is saved, so "guardrails explain"
shows it again.

--staged gates only files staged for commit, for pre-commit hooks.`,
		Example: `  guardrails gate
  guardrails gate --base main     # everything this branch changed
  guardrails gate --all --json    # every package, for CI
  guardrails gate --staged        # in a pre-commit hook`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.gate(cmd.Context())
		},
	}
	f := cmd.Flags()
	f.Bool("all", false, "gate every package, not just changed files")
	f.String("base", "HEAD", "git ref that changes and patch coverage are measured against")
	f.Bool("explain", false, "print the full, untruncated report")
	f.Bool("json", false, "print checks as JSON")
	f.Bool("staged", false, "gate only files staged for commit; patch coverage is measured against HEAD")
	a.bind(cmd, "gate")
	return cmd
}

func (a *App) gate(ctx context.Context) error {
	all, base, staged := a.V.GetBool("gate.all"), a.V.GetString("gate.base"), a.V.GetBool("gate.staged")
	if staged && all {
		return usageError{errors.New("--staged and --all are mutually exclusive")}
	}
	if staged {
		base = "HEAD"
	}
	e := a.engine()
	wd, err := a.Getwd()
	if err != nil {
		return err
	}
	repo := e.RepoRoot(ctx, wd)
	cfg, err := config.Load(a.globalConfig(), repo)
	if err != nil {
		return err
	}
	var files []string
	switch {
	case staged:
		files = a.stagedFiles(ctx, repo)
		if len(files) == 0 {
			fmt.Fprintln(a.Stdout, "guardrails: no staged changes")
			return nil
		}
	case !all:
		files = a.changedFiles(ctx, repo, base)
		if len(files) == 0 {
			fmt.Fprintf(a.Stdout, "guardrails: no changes against %s; use --all to gate everything\n", base)
			return nil
		}
	}
	rc, _ := e.Store.LoadRepo(repo)
	var baseline map[string]float64
	if rc != nil {
		baseline = rc.Coverage
	}
	patchBase := ""
	if !all {
		patchBase = base
	}
	g := e.Gate(ctx, repo, cfg, files, patchBase, baseline, all)
	report := feedback.Report{Checks: g.Checks}
	failed := report.Failed()
	passed := len(failed) == 0

	reportPath := filepath.Join(a.DataDir(), "reports", state.Key(repo)+".txt")
	report.Title = fmt.Sprintf("guardrails: %s gate %s", strings.Join(g.Modules, "+"), passFail(passed, len(failed), g.Ran))
	rendered := feedback.Render(report, feedback.Options{OverflowPath: reportPath})
	_ = os.MkdirAll(filepath.Dir(reportPath), 0o700)
	_ = os.WriteFile(reportPath, []byte(rendered.Full), 0o600)
	e.RecordGate(repo, reportPath, passed, g.Total, g.Coverage, cfg, g.Checks)

	asJSON := a.V.GetBool("gate.json")
	switch {
	case asJSON:
		enc := json.NewEncoder(a.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(map[string]any{"passed": passed, "coverage": g.Total, "checks": failed, "notes": g.Notes})
	case a.V.GetBool("gate.explain"):
		fmt.Fprint(a.Stdout, rendered.Full)
	default:
		fmt.Fprint(a.Stdout, rendered.Text)
	}
	if !asJSON {
		for _, n := range g.Notes {
			fmt.Fprintln(a.Stderr, "note: "+n)
		}
		if passed && g.Total >= 0 {
			fmt.Fprintf(a.Stdout, "coverage %.1f%% (threshold %.0f%%)\n", g.Total, cfg.Go.Coverage.Total)
		}
	}
	if !passed {
		return exitCode(1)
	}
	return nil
}

func passFail(passed bool, failed, ran int) string {
	if passed {
		return fmt.Sprintf("passed (%d checks)", ran)
	}
	return fmt.Sprintf("failed (%d of %d checks)", failed, max(ran, failed))
}

// changedFiles lists files changed against base plus untracked files, as
// absolute paths.
func (a *App) changedFiles(ctx context.Context, repo, base string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(stdout string) {
		for _, rel := range trimLines(stdout) {
			p := filepath.Join(repo, filepath.FromSlash(rel))
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	if res, err := a.Runner.Run(ctx, repo, "git", "diff", "--name-only", "--diff-filter=d", base); err == nil && res.ExitCode == 0 {
		add(res.Stdout)
	}
	if res, err := a.Runner.Run(ctx, repo, "git", "ls-files", "--others", "--exclude-standard"); err == nil && res.ExitCode == 0 {
		add(res.Stdout)
	}
	return out
}

// stagedFiles lists files staged for commit, excluding deletions, as
// absolute paths. Tests and coverage still run on the working tree, so
// unstaged edits in the same packages are part of what gets checked.
func (a *App) stagedFiles(ctx context.Context, repo string) []string {
	res, err := a.Runner.Run(ctx, repo, "git", "diff", "--cached", "--name-only", "--diff-filter=d")
	if err != nil || res.ExitCode != 0 {
		return nil
	}
	var out []string
	for _, rel := range trimLines(res.Stdout) {
		out = append(out, filepath.Join(repo, filepath.FromSlash(rel)))
	}
	return out
}
