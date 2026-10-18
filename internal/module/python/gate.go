package python

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rossedman/guardrails/internal/diff"
	"github.com/rossedman/guardrails/internal/feedback"
	"github.com/rossedman/guardrails/internal/module"
	"github.com/rossedman/guardrails/internal/runner"
	"github.com/rossedman/guardrails/internal/tools"
)

// ratchetTolerance absorbs float noise when comparing to the baseline.
const ratchetTolerance = 0.05

// keyPrefix namespaces Python packages in the shared coverage baseline.
const keyPrefix = "python:"

// Gate implements module.Module.
func (m *Module) Gate(ctx context.Context, env module.Env, in module.GateInput) module.GateResult {
	res := module.GateResult{Coverage: map[string]float64{}, Total: -1}
	files := pyFiles(in.Files)
	if in.All {
		files = allPyFiles(env.Repo)
	}
	groups := byProject(env.Repo, files)
	projects := make([]string, 0, len(groups))
	for p := range groups {
		projects = append(projects, p)
	}
	sort.Strings(projects)

	var covered, total int
	for _, proj := range projects {
		r := m.gateProject(ctx, env, proj, groups[proj], in)
		res.Checks = append(res.Checks, r.checks...)
		res.Notes = append(res.Notes, r.notes...)
		res.Ran = appendUnique(res.Ran, r.ran...)
		for k, v := range r.coverage {
			res.Coverage[k] = v
		}
		if r.total >= 0 {
			covered += r.covered
			total += r.statements
		}
	}
	if total > 0 {
		res.Total = float64(covered) * 100 / float64(total)
	}
	return res
}

type projectResult struct {
	ran                 []string
	checks              []feedback.Check
	notes               []string
	coverage            map[string]float64
	total               float64
	covered, statements int
}

func (m *Module) gateProject(ctx context.Context, env module.Env, proj string, files []string, in module.GateInput) projectResult {
	out := projectResult{coverage: map[string]float64{}, total: -1}
	var want map[string]bool
	if !in.All {
		want = fileSet(files)
	}

	// 1. ruff over touched files.
	out.ran = append(out.ran, "ruff")
	if _, err := resolve(ctx, env, proj, "ruff"); err != nil {
		out.checks = append(out.checks, m.unavailableCheck("ruff", err))
	} else if c, ok := m.ruffCheck(ctx, env, proj, files, fileSet(files)); ok {
		out.checks = append(out.checks, c)
	}

	// 2. Tests with coverage.
	out.ran = append(out.ran, "tests", "coverage")
	m.testAndCover(ctx, env, proj, files, in, &out)

	// 3. Type check touched packages.
	if checker := env.Config.Python.Typecheck; checker != "off" {
		out.ran = append(out.ran, checker)
		c, ok, uerr := m.typecheck(ctx, env, proj, packagesOf(files), want)
		switch {
		case uerr != nil:
			out.checks = append(out.checks, m.unavailableCheck(checker, uerr))
		case ok:
			out.checks = append(out.checks, c)
		}
	}

	// 4. uv.lock is current.
	if _, err := os.Stat(filepath.Join(proj, "uv.lock")); err == nil {
		out.ran = append(out.ran, "lock")
		uv, err := env.Tool(ctx, "uv")
		if err != nil {
			out.notes = append(out.notes, module.Unavailable(m.tool("uv"), err)+"; uv.lock check skipped")
		} else if r, err := run(ctx, env, proj, uv, "lock", "--check"); err != nil {
			out.notes = append(out.notes, "uv lock --check could not run: "+err.Error())
		} else if r.ExitCode != 0 {
			out.checks = append(out.checks, feedback.Check{
				Name: "lock", Failed: true, Summary: "uv.lock is out of date with pyproject.toml",
				Lines: firstLines(r.Combined(), 6),
				Fix:   "run `uv lock`",
			})
		}
	}
	return out
}

// testCommand picks how to run pytest in the project's environment.
func (m *Module) testCommand(ctx context.Context, env module.Env, proj string) (tools.Command, []string, error) {
	pytest := []string{"--with", "pytest", "--with", "pytest-cov", "pytest"}
	uv, uvErr := env.Tool(ctx, "uv")
	venvPy := venvPython(env.Repo, proj)
	_, lockErr := os.Stat(filepath.Join(proj, "uv.lock"))
	switch {
	case uvErr == nil && lockErr == nil:
		return uv, append([]string{"run", "--frozen"}, pytest...), nil
	case uvErr == nil && venvPy != "":
		return uv, append([]string{"run", "--no-project", "--python", venvPy}, pytest...), nil
	case uvErr == nil:
		args := []string{"run", "--no-project"}
		if hasProjectTable(proj) {
			args = append(args, "--with-editable", proj)
		}
		return uv, append(args, pytest...), nil
	case venvPy != "":
		return tools.Command{Path: venvPy}, []string{"-m", "pytest"}, nil
	case module.IsUnavailable(uvErr):
		return tools.Command{}, nil, uvErr
	default:
		return tools.Command{}, nil, errNoEnv
	}
}

func (m *Module) testAndCover(ctx context.Context, env module.Env, proj string, files []string, in module.GateInput, out *projectResult) {
	cmd, prefix, err := m.testCommand(ctx, env, proj)
	if err != nil {
		c := m.unavailableCheck("uv", err)
		if !module.IsUnavailable(err) {
			c.Summary = err.Error()
		}
		out.checks = append(out.checks, c)
		return
	}
	tmp, err := os.CreateTemp("", "guardrails-pycov-*.json")
	if err != nil {
		out.checks = append(out.checks, toolError("tests", err))
		return
	}
	report := tmp.Name()
	_ = tmp.Close()
	_ = os.Remove(report)
	defer func() { _ = os.Remove(report) }()

	// Keep coverage's data file out of the project, via env(1) since the
	// runner passes the environment through unchanged.
	data := report + ".data"
	defer func() { _ = os.Remove(data) }()
	args := append([]string{"COVERAGE_FILE=" + data, cmd.Path}, cmd.Args...)
	args = append(args, prefix...)
	args = append(args, "--cov=.", "--cov-report=json:"+report, "-q", "-rfE", "-p", "no:cacheprovider")
	res, err := env.Runner.Run(ctx, proj, "env", args...)
	if err != nil {
		out.checks = append(out.checks, toolError("tests", err))
		return
	}
	// pytest: 0 passed, 1 failures, 5 no tests collected.
	if res.ExitCode != 0 && res.ExitCode != 5 {
		failed := failedTests(res.Combined())
		summary := fmt.Sprintf("pytest exited %d", res.ExitCode)
		if len(failed) > 0 {
			summary = fmt.Sprintf("%d failing test%s", len(failed), plural(len(failed)))
		}
		c := feedback.Check{
			Name: "tests", Failed: true, Summary: summary, Lines: capList(failed, 10),
			Fix: "make the failing tests pass; do not skip or delete them",
		}
		if len(failed) == 0 {
			c.Lines = lastLines(res.Combined(), 15)
		}
		out.checks = append(out.checks, c)
		return
	}
	raw, err := os.ReadFile(report)
	if err != nil && noTests(res) {
		out.checks = append(out.checks, feedback.Check{
			Name: "coverage", Failed: true,
			Summary: fmt.Sprintf("no tests ran, so touched code has 0%% coverage (< %.0f%%)", env.Config.Python.Coverage.Total),
			Lines:   relPaths(env.Repo, files),
			Fix:     "add pytest tests (test_*.py) that exercise the files above",
		})
		return
	}
	if err != nil {
		out.checks = append(out.checks, feedback.Check{
			Name: "coverage", Failed: true, Summary: "pytest-cov wrote no report",
			Lines: lastLines(res.Combined(), 10),
			Fix:   "make sure pytest-cov can run in the project environment",
		})
		return
	}
	cov, err := parseCoverage(raw)
	if err != nil {
		out.checks = append(out.checks, toolError("coverage", err))
		return
	}
	m.coverageChecks(ctx, env, proj, files, cov, in, out)
}

func (m *Module) coverageChecks(ctx context.Context, env module.Env, proj string, files []string, cov *covReport, in module.GateInput, out *projectResult) {
	cfg := env.Config
	type stats struct{ covered, total int }
	byPkg := map[string]*stats{}
	var tot stats
	type fileStat struct {
		path    string
		covered int
		total   int
		missing []int
	}
	var fstats []fileStat
	for name, f := range cov.Files {
		abs := absTo(proj, name)
		key := keyPrefix + displayPath(env.Repo, filepath.Dir(abs))
		s := byPkg[key]
		if s == nil {
			s = &stats{}
			byPkg[key] = s
		}
		s.covered += f.Summary.CoveredLines
		s.total += f.Summary.NumStatements
		tot.covered += f.Summary.CoveredLines
		tot.total += f.Summary.NumStatements
		fstats = append(fstats, fileStat{displayPath(env.Repo, abs), f.Summary.CoveredLines, f.Summary.NumStatements, f.MissingLines})
	}
	pct := func(c, t int) float64 {
		if t == 0 {
			return 100
		}
		return float64(c) * 100 / float64(t)
	}
	for k, s := range byPkg {
		out.coverage[k] = pct(s.covered, s.total)
	}
	out.covered, out.statements = tot.covered, tot.total
	out.total = pct(tot.covered, tot.total)

	var lines []string
	if cfg.Python.Coverage.Ratchet {
		keys := make([]string, 0, len(byPkg))
		for k := range byPkg {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if base, ok := in.Baseline[k]; ok && out.coverage[k]+ratchetTolerance < base {
				lines = append(lines, fmt.Sprintf("%s  %.1f%% dropped below its session baseline of %.1f%%", strings.TrimPrefix(k, keyPrefix), out.coverage[k], base))
			}
		}
	}
	below := out.total+1e-9 < cfg.Python.Coverage.Total
	if below || len(lines) > 0 {
		sort.Slice(fstats, func(i, j int) bool {
			mi, mj := fstats[i].total-fstats[i].covered, fstats[j].total-fstats[j].covered
			if mi != mj {
				return mi > mj
			}
			return fstats[i].path < fstats[j].path
		})
		for i, f := range fstats {
			if i == 5 || f.total == f.covered {
				break
			}
			lines = append(lines, fmt.Sprintf("%s  %d/%d stmts  missing %s", f.path, f.covered, f.total, ranges(f.missing)))
		}
		summary := fmt.Sprintf("%.1f%% total", out.total)
		if below {
			summary = fmt.Sprintf("%.1f%% < %.1f%%", out.total, cfg.Python.Coverage.Total)
		}
		if proj != env.Repo {
			summary += " (" + displayPath(env.Repo, proj) + ")"
		}
		out.checks = append(out.checks, feedback.Check{
			Name: "coverage", Failed: true, Summary: summary,
			Lines: lines, Fix: "add pytest tests that exercise the missing lines above",
		})
	}

	if in.BaseCommit == "" || cfg.Python.PatchCoverage <= 0 {
		return
	}
	changed := changedLines(ctx, env, in.BaseCommit, files)
	if len(changed) == 0 {
		return
	}
	out.ran = append(out.ran, "patch-coverage")
	var pc, pt int
	uncovered := map[string][]int{}
	for name, f := range cov.Files {
		abs := absTo(proj, name)
		lines := changed[abs]
		if len(lines) == 0 {
			continue
		}
		for _, l := range f.ExecutedLines {
			if lines[l] {
				pc++
				pt++
			}
		}
		for _, l := range f.MissingLines {
			if lines[l] {
				pt++
				uncovered[abs] = append(uncovered[abs], l)
			}
		}
	}
	if pt == 0 || pct(pc, pt)+1e-9 >= cfg.Python.PatchCoverage {
		return
	}
	paths := make([]string, 0, len(uncovered))
	for p := range uncovered {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var plines []string
	for _, p := range paths {
		ls := uncovered[p]
		sort.Ints(ls)
		plines = append(plines, displayPath(env.Repo, p)+":"+ranges(ls))
	}
	out.checks = append(out.checks, feedback.Check{
		Name:    "patch-coverage",
		Failed:  true,
		Summary: fmt.Sprintf("%.1f%% of changed lines covered < %.1f%% (%d/%d)", pct(pc, pt), cfg.Python.PatchCoverage, pc, pt),
		Lines:   plines,
		Fix:     "test the changed lines above; they are code you wrote this session",
	})
}

// changedLines returns lines changed since base in the touched non-test
// Python files, keyed by absolute path. Untracked files count as new.
func changedLines(ctx context.Context, env module.Env, base string, files []string) map[string]map[int]bool {
	out := map[string]map[int]bool{}
	var targets []string
	for _, f := range files {
		if !isTestFile(f) {
			targets = append(targets, f)
		}
	}
	if len(targets) == 0 {
		return out
	}
	args := append([]string{"diff", "--unified=0", "--no-color", "--no-ext-diff", "--src-prefix=a/", "--dst-prefix=b/", base, "--"}, targets...)
	if res, err := env.Runner.Run(ctx, env.Repo, "git", args...); err == nil && res.ExitCode == 0 {
		for rel, set := range diff.ParseUnified(res.Stdout) {
			out[filepath.Join(env.Repo, filepath.FromSlash(rel))] = set
		}
	}
	if res, err := env.Runner.Run(ctx, env.Repo, "git", append([]string{"ls-files", "--others", "--exclude-standard", "--"}, targets...)...); err == nil && res.ExitCode == 0 {
		for _, rel := range strings.Fields(res.Stdout) {
			abs := filepath.Join(env.Repo, filepath.FromSlash(rel))
			out[abs] = allLines(abs)
		}
	}
	return out
}

func allLines(path string) map[int]bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	set := map[int]bool{}
	for i := range strings.Split(string(data), "\n") {
		set[i+1] = true
	}
	return set
}

// hasProjectTable reports whether proj's pyproject.toml declares a
// [project], so it can be installed for its tests.
func hasProjectTable(proj string) bool {
	data, err := os.ReadFile(filepath.Join(proj, "pyproject.toml"))
	if err != nil {
		return false
	}
	for _, l := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(l) == "[project]" {
			return true
		}
	}
	return false
}

// packagesOf returns the sorted directories of files.
func packagesOf(files []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range files {
		d := filepath.Dir(f)
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out
}

// allPyFiles lists every .py file under repo, skipping virtualenvs and
// hidden or vendored directories.
func allPyFiles(repo string) []string {
	var out []string
	_ = filepath.WalkDir(repo, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if p != repo && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "__pycache__" || name == "venv" || name == "site-packages") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(p, ".py") {
			out = append(out, p)
		}
		return nil
	})
	return out
}

// ranges renders sorted lines compactly, e.g. "12-14,20".
func ranges(ls []int) string {
	var parts []string
	for i := 0; i < len(ls); {
		j := i
		for j+1 < len(ls) && ls[j+1] == ls[j]+1 {
			j++
		}
		if i == j {
			parts = append(parts, fmt.Sprint(ls[i]))
		} else {
			parts = append(parts, fmt.Sprintf("%d-%d", ls[i], ls[j]))
		}
		i = j + 1
	}
	return strings.Join(parts, ",")
}

func capList(items []string, n int) []string {
	if len(items) <= n {
		return items
	}
	return append(items[:n:n], fmt.Sprintf("+%d more", len(items)-n))
}

func appendUnique(dst []string, items ...string) []string {
	for _, it := range items {
		found := false
		for _, d := range dst {
			if d == it {
				found = true
				break
			}
		}
		if !found {
			dst = append(dst, it)
		}
	}
	return dst
}

// noTests reports whether pytest collected nothing (exit code 5).
func noTests(res runner.Result) bool {
	return res.ExitCode == 5 || strings.Contains(res.Combined(), "no tests ran")
}

// relPaths renders files relative to the repo.
func relPaths(repo string, files []string) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		if rel, err := filepath.Rel(repo, f); err == nil {
			f = filepath.ToSlash(rel)
		}
		out = append(out, f)
	}
	return out
}
