package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rossedman/guardrails/internal/config"
	"github.com/rossedman/guardrails/internal/feedback"
	"github.com/rossedman/guardrails/internal/module"
	"github.com/rossedman/guardrails/internal/module/golang"
	"github.com/rossedman/guardrails/internal/testdepth"
)

func newHardenCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "harden",
		Short: "Mutation-test and fuzz the Go code you changed (on demand)",
		Long: `Check how well the tests of the Go functions changed against --base
actually test them, on demand rather than in the Stop gate:

  mutation testing  flips operators and constants in each changed function
                    and checks a test fails; survivors are reported with
                    their line, against go.mutation.min_score
  fuzzing           runs the fuzz targets in touched packages for
                    go.fuzz.fuzztime each
  suggestions       benchmarks and fuzz targets worth adding (not failures)

It exits 1 when the mutation score is low or a fuzz target fails. The
/guardrails:harden skill runs it and has Claude fix what it finds.`,
		Example: `  guardrails harden --base main
  guardrails harden --no-fuzz --min-score 80
  guardrails harden --budget 5m --json`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.harden(cmd.Context())
		},
	}
	f := cmd.Flags()
	f.String("base", "HEAD", "git ref to compare against, e.g. main to review a whole branch")
	f.Duration("budget", 0, "time budget for mutation testing (default go.mutation.budget_seconds)")
	f.Float64("min-score", -1, "minimum mutation score (default go.mutation.min_score)")
	f.Duration("fuzztime", 0, "fuzzing time per target (default go.fuzz.fuzztime)")
	f.Bool("no-mutation", false, "skip mutation testing")
	f.Bool("no-fuzz", false, "skip fuzzing")
	f.Bool("json", false, "print results as JSON")
	a.bind(cmd, "harden")
	return cmd
}

// harden mutation-tests and fuzzes the Go functions changed against a base
// ref, and suggests benchmarks and fuzz targets. It is advisory tooling for
// a human or the /guardrails:harden skill, not part of the Stop gate.
func (a *App) harden(ctx context.Context) error {
	v := a.V
	base := v.GetString("harden.base")
	budget, minScore, fuzztime := v.GetDuration("harden.budget"), v.GetFloat64("harden.min-score"), v.GetDuration("harden.fuzztime")
	noMutation, noFuzz, asJSON := v.GetBool("harden.no-mutation"), v.GetBool("harden.no-fuzz"), v.GetBool("harden.json")
	repo, err := a.repo(ctx)
	if err != nil {
		return err
	}
	cfg, err := config.Load(a.globalConfig(), repo)
	if err != nil {
		return err
	}
	if budget > 0 {
		cfg.Go.Mutation.BudgetSeconds = int(budget.Seconds())
	}
	if minScore >= 0 {
		cfg.Go.Mutation.MinScore = minScore
	}
	if fuzztime > 0 {
		cfg.Go.Fuzz.Fuzztime = fuzztime.String()
	}

	changed, touched, err := testdepth.Changes(ctx, a.Runner, repo, base)
	if err != nil {
		return err
	}
	funcs, err := testdepth.ChangedFuncs(changed)
	if err != nil {
		return err
	}
	if len(funcs) == 0 && len(touched) == 0 {
		fmt.Fprintf(a.Stdout, "guardrails harden: no Go changes against %s; try --base main\n", base)
		return nil
	}

	byRoot := map[string][]testdepth.Func{}
	touchedByRoot := map[string][]string{}
	for _, f := range funcs {
		if root := golang.FindModuleRoot(f.Dir); root != "" {
			byRoot[root] = append(byRoot[root], f)
		}
	}
	for _, p := range touched {
		if root := golang.FindModuleRoot(filepath.Dir(p)); root != "" {
			touchedByRoot[root] = append(touchedByRoot[root], p)
		}
	}
	roots := make([]string, 0, len(touchedByRoot))
	for r := range touchedByRoot {
		roots = append(roots, r)
	}
	sort.Strings(roots)

	var (
		res         testdepth.Result
		suggestions []string
	)
	for _, root := range roots {
		modPath, err := golang.ModulePath(root)
		if err != nil {
			return err
		}
		e := testdepth.Env{
			Env:     module.Env{Runner: a.Runner, Config: cfg, Repo: repo, DataDir: a.DataDir()},
			Root:    root,
			ModPath: modPath,
			Base:    base,
			Changed: changed,
			Now:     a.Now,
		}
		fs := byRoot[root]
		if !noMutation && len(fs) > 0 {
			res.Merge(testdepth.Mutation(ctx, e, fs))
		}
		if !noFuzz {
			res.Merge(testdepth.Fuzz(ctx, e, fs, touchedByRoot[root]))
		}
		suggestions = append(suggestions, testdepth.BenchSuggestions(e, fs, cfg.Go.Bench.Packages)...)
		suggestions = append(suggestions, testdepth.FuzzSuggestions(e, fs)...)
	}

	report := feedback.Report{Checks: res.Checks}
	failed := report.Failed()
	if asJSON {
		enc := json.NewEncoder(a.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(map[string]any{
			"passed": len(failed) == 0, "functions": len(funcs), "checks": failed,
			"suggestions": suggestions, "notes": res.Notes, "ran": res.Ran,
		})
	} else {
		a.printHarden(report, failed, len(funcs), base, res, suggestions)
	}
	if len(failed) > 0 {
		return exitCode(1)
	}
	return nil
}

func (a *App) printHarden(report feedback.Report, failed []feedback.Check, funcs int, base string, res testdepth.Result, suggestions []string) {
	ran := "nothing to run"
	if len(res.Ran) > 0 {
		ran = strings.Join(uniq(res.Ran), ", ")
	}
	status := "passed"
	if len(failed) > 0 {
		status = fmt.Sprintf("found %d problem%s", len(failed), map[bool]string{true: "", false: "s"}[len(failed) == 1])
	}
	report.Title = fmt.Sprintf("guardrails harden: %s (%d changed functions vs %s; ran %s)", status, funcs, base, ran)
	fmt.Fprint(a.Stdout, feedback.Render(report, feedback.Options{MaxIssues: 1 << 20, MaxChars: 1 << 30}).Full)
	if len(suggestions) > 0 {
		fmt.Fprintf(a.Stdout, "\nsuggestions (not failures):\n")
		for _, s := range suggestions {
			fmt.Fprintf(a.Stdout, "  %s\n", s)
		}
	}
	for _, n := range res.Notes {
		fmt.Fprintln(a.Stderr, "note: "+n)
	}
}

func uniq(items []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, it := range items {
		if !seen[it] {
			seen[it] = true
			out = append(out, it)
		}
	}
	return out
}
