package testdepth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rossedman/guardrails/internal/feedback"
)

// benchSignificance is the p-value below which a change counts.
const benchSignificance = 0.05

// Benchmarks requires benchmarks for changed hot-path functions and, when
// benchmarks cover changed code, compares them against the base commit.
func Benchmarks(ctx context.Context, e Env, funcs []Func) Result {
	cfg := e.Config.Go.Bench
	budget := time.Duration(cfg.BudgetSeconds) * time.Second
	ctx, cancel := Slice(ctx, budget)
	defer cancel()

	var res Result
	idx := benchIndex{}
	if cfg.RequireOnHotPaths && len(funcs) > 0 {
		res.Ran = append(res.Ran, "benchmarks")
		benchRequirements(e, funcs, idx, &res)
	}
	if e.Base != "" {
		benchRegression(ctx, e, funcs, idx, &res)
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			res.Notes = append(res.Notes, fmt.Sprintf("benchmark comparison skipped: exceeded the %ds budget", cfg.BudgetSeconds))
		}
	}
	return res
}

// benchIndex caches IndexTests per package directory.
type benchIndex map[string]*Tests

func (idx benchIndex) get(dir string) (*Tests, error) {
	if t, ok := idx[dir]; ok {
		return t, nil
	}
	t, err := IndexTests(dir)
	if err != nil {
		return nil, err
	}
	idx[dir] = t
	return t, nil
}

func benchRequirements(e Env, funcs []Func, idx benchIndex, res *Result) {
	var lines []string
	for _, f := range funcs {
		if _, ok := f.Annotations["nobench"]; ok {
			continue
		}
		sig := HotPath(f, e.Root, e.Config.Go.Bench.Packages)
		if !sig.Any() {
			continue
		}
		t, err := idx.get(f.Dir)
		if err != nil {
			res.Notes = append(res.Notes, "benchmark index skipped: "+err.Error())
			continue
		}
		if len(Covering(t.Benchmarks, f)) > 0 {
			continue
		}
		line := fmt.Sprintf("%s:%d  %s  needs a Benchmark (%s)", benchDisplay(e, f.File), f.Start, f.Name, strings.Join(sig.Reasons, "; "))
		if !sig.Strong {
			key := "bench:" + f.Key(e.Root)
			if e.Asked[key] {
				continue
			}
			res.Asked = append(res.Asked, key)
			line += " — or say in one line why not"
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return
	}
	summary := fmt.Sprintf("%d hot-path functions have no benchmark", len(lines))
	if len(lines) == 1 {
		summary = "1 hot-path function has no benchmark"
	}
	res.Checks = append(res.Checks, feedback.Check{
		Name: "benchmarks", Failed: true, Summary: summary, Lines: lines,
		Fix: "add Benchmark functions that call these (b.ReportAllocs, b.Loop or b.N); the human can opt a function out with // guardrails:nobench <reason>",
	})
}

// benchRegression runs the benchmarks covering changed functions at the
// head and at the base commit and reports significant slowdowns.
func benchRegression(ctx context.Context, e Env, funcs []Func, idx benchIndex, res *Result) {
	byDir := map[string]map[string]bool{}
	for _, f := range funcs {
		t, err := idx.get(f.Dir)
		if err != nil {
			continue
		}
		for _, b := range Covering(t.Benchmarks, f) {
			if byDir[f.Dir] == nil {
				byDir[f.Dir] = map[string]bool{}
			}
			byDir[f.Dir][b] = true
		}
	}
	if len(byDir) == 0 {
		return
	}
	res.Ran = append(res.Ran, "bench-regression")

	dirs := make([]string, 0, len(byDir))
	for d := range byDir {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)

	head := benchSamples{}
	for _, dir := range dirs {
		out, err := benchRun(ctx, e, dir, byDir[dir])
		if err != nil {
			if ctx.Err() == nil {
				res.Notes = append(res.Notes, "benchmarks failed to run: "+err.Error())
			}
			return
		}
		for k, v := range out {
			head[k] = v
		}
	}

	base, err := benchAtBase(ctx, e, dirs, byDir)
	if err != nil {
		if ctx.Err() == nil {
			res.Notes = append(res.Notes, "benchmark comparison skipped: "+err.Error())
		}
		return
	}

	if c, ok := benchCompare(e, base, head); ok {
		res.Checks = append(res.Checks, c)
	}
}

// benchRun runs the named benchmarks of the package at dir from the module
// root e.Root.
func benchRun(ctx context.Context, e Env, dir string, names map[string]bool) (benchSamples, error) {
	rel, err := filepath.Rel(e.Root, dir)
	if err != nil {
		return nil, err
	}
	list := make([]string, 0, len(names))
	for n := range names {
		list = append(list, n)
	}
	sort.Strings(list)
	cfg := e.Config.Go.Bench
	args := []string{
		"test", "-run", "^$", "-bench", benchRegex(list), "-benchmem",
		fmt.Sprintf("-count=%d", cfg.Count), "-benchtime=" + cfg.Benchtime,
		"./" + filepath.ToSlash(rel),
	}
	r, err := e.Runner.Run(ctx, e.Root, "go", args...)
	if err != nil {
		return nil, err
	}
	if r.ExitCode != 0 {
		return nil, fmt.Errorf("go test -bench in %s exited %d: %s", benchDisplay(e, dir), r.ExitCode, benchFirstLine(r.Combined()))
	}
	return benchParse(r.Stdout), nil
}

// benchAtBase runs the benchmarks in a detached worktree of the base
// commit, leaving the user's working tree untouched. Packages or
// benchmarks that don't exist at the base are skipped.
func benchAtBase(ctx context.Context, e Env, dirs []string, byDir map[string]map[string]bool) (benchSamples, error) {
	tmp, err := os.MkdirTemp("", "guardrails-bench-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	r, err := e.Runner.Run(ctx, e.Repo, "git", "worktree", "add", "--detach", tmp, e.Base)
	if err != nil {
		return nil, err
	}
	if r.ExitCode != 0 {
		return nil, fmt.Errorf("git worktree add: %s", benchFirstLine(r.Combined()))
	}
	defer func() {
		// Cleanup must run even when ctx has expired.
		bg := context.WithoutCancel(ctx)
		_, _ = e.Runner.Run(bg, e.Repo, "git", "worktree", "remove", "--force", tmp)
		_, _ = e.Runner.Run(bg, e.Repo, "git", "worktree", "prune")
	}()

	relRoot, err := filepath.Rel(e.Repo, e.Root)
	if err != nil {
		return nil, err
	}
	baseRoot := filepath.Join(tmp, relRoot)
	out := benchSamples{}
	for _, dir := range dirs {
		rel, err := filepath.Rel(e.Root, dir)
		if err != nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(baseRoot, rel)); err != nil {
			continue
		}
		baseEnv := e
		baseEnv.Root = baseRoot
		s, err := benchRun(ctx, baseEnv, filepath.Join(baseRoot, rel), byDir[dir])
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		for k, v := range s {
			out[k] = v
		}
	}
	return out, nil
}

// benchCompare reports metrics that got significantly worse.
func benchCompare(e Env, base, head benchSamples) (feedback.Check, bool) {
	threshold := e.Config.Go.Bench.RegressionPct
	var lines []string
	regressed := map[string]bool{}
	for _, name := range head.benchNames() {
		old, ok := base[name]
		if !ok {
			continue
		}
		for _, unit := range benchUnitOrder(head[name]) {
			hv, bv := head[name][unit], old[unit]
			if len(hv) == 0 || len(bv) == 0 {
				continue
			}
			p := benchMannWhitney(bv, hv)
			if p >= benchSignificance {
				continue
			}
			bm, hm := benchMedian(bv), benchMedian(hv)
			var delta string
			switch {
			case bm == 0 && hm > 0 && (unit == "B/op" || unit == "allocs/op"):
				delta = "+∞%"
			case bm > 0 && (hm/bm-1)*100 > threshold:
				delta = fmt.Sprintf("%+.1f%%", (hm/bm-1)*100)
			default:
				continue
			}
			regressed[name] = true
			lines = append(lines, fmt.Sprintf("%s  %s  %s (p=%.3f)  %s → %s",
				name, benchMetricName(unit), delta, p, benchFormat(unit, bm), benchFormat(unit, hm)))
		}
	}
	if len(lines) == 0 {
		return feedback.Check{}, false
	}
	summary := fmt.Sprintf("%d benchmarks regressed vs the session's base commit", len(regressed))
	if len(regressed) == 1 {
		summary = "1 benchmark regressed vs the session's base commit"
	}
	return feedback.Check{
		Name: "bench-regression", Failed: true, Summary: summary, Lines: lines,
		Fix: "make the changed code at least as fast as before, or explain the trade-off to the human",
	}, true
}

func benchDisplay(e Env, path string) string {
	if rel, err := filepath.Rel(e.Repo, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(path)
}

func benchFirstLine(s string) string {
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}
