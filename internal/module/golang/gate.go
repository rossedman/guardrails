package golang

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/rossedman/guardrails/internal/coverage"
	"github.com/rossedman/guardrails/internal/diff"
	"github.com/rossedman/guardrails/internal/feedback"
	"github.com/rossedman/guardrails/internal/glob"
	"github.com/rossedman/guardrails/internal/module"
)

// ratchetTolerance absorbs float noise when comparing to the baseline.
const ratchetTolerance = 0.05

// Gate implements module.Module.
func (m *Module) Gate(ctx context.Context, env module.Env, in module.GateInput) module.GateResult {
	res := module.GateResult{Coverage: map[string]float64{}, Total: -1}
	if _, err := env.Runner.LookPath("go"); err != nil {
		res.Checks = append(res.Checks, feedback.Check{
			Name: "tools", Failed: true, Summary: "the go toolchain is not installed",
			Fix: "the human must install Go (https://go.dev/doc/install)",
		})
		return res
	}
	files := in.Files
	if in.All {
		files = allGoFiles(env.Repo)
	}
	roots := byModule(files)
	keys := make([]string, 0, len(roots))
	for r := range roots {
		keys = append(keys, r)
	}
	sort.Strings(keys)
	var all coverage.Stats
	measured := false
	for _, root := range keys {
		r := m.gateModule(ctx, env, root, roots[root], in)
		res.Checks = append(res.Checks, r.checks...)
		res.Notes = append(res.Notes, r.notes...)
		res.Ran = appendUnique(res.Ran, r.ran...)
		for pkg, pct := range r.coverage {
			res.Coverage[pkg] = pct
		}
		if r.measured {
			measured = true
			all.Covered += r.total.Covered
			all.Total += r.total.Total
		}
	}
	if measured {
		res.Total = all.Percent()
	}
	return res
}

type moduleResult struct {
	ran      []string
	checks   []feedback.Check
	notes    []string
	coverage map[string]float64
	total    coverage.Stats
	measured bool
}

func (m *Module) gateModule(ctx context.Context, env module.Env, root string, files []string, in module.GateInput) moduleResult {
	out := moduleResult{coverage: map[string]float64{}}
	cfg := env.Config.Go

	// 1. Build.
	out.ran = append(out.ran, "build")
	build, err := env.Runner.Run(ctx, root, "go", "build", "./...")
	if err != nil {
		out.checks = append(out.checks, toolError("build", err))
		return out
	}
	if build.ExitCode != 0 {
		out.checks = append(out.checks, feedback.Check{
			Name: "build", Failed: true, Summary: "go build ./... failed",
			Issues: relIssues(env.Repo, root, module.ParseLocations(build.Combined(), "compile")),
			Lines:  linesIfNoIssues(build.Combined()),
			Fix:    "make the module compile before anything else",
		})
		return out
	}

	pkgs := packagesOf(root, files)
	if in.All {
		pkgs = []string{"./..."}
	}
	modPath, err := ModulePath(root)
	if err != nil {
		out.checks = append(out.checks, toolError("build", err))
		return out
	}

	// 2. Tests with coverage.
	if len(pkgs) > 0 {
		out.ran = append(out.ran, "tests", "coverage")
		m.testAndCover(ctx, env, root, modPath, pkgs, files, in, &out)
	}

	// 3. Full lint on touched packages.
	if len(pkgs) > 0 {
		var want map[string]bool
		if !in.All {
			want = fileSet(files)
		}
		out.ran = append(out.ran, "lint")
		if c, ok := m.lint(ctx, env, root, append([]string{"run"}, pkgs...), want); ok {
			out.checks = append(out.checks, c)
		}
	}

	// 4. go mod tidy produces no diff.
	out.ran = append(out.ran, "mod-tidy")
	tidy, err := env.Runner.Run(ctx, root, "go", "mod", "tidy", "-diff")
	switch {
	case err != nil:
		out.notes = append(out.notes, "go mod tidy -diff could not run: "+err.Error())
	case tidy.ExitCode != 0 && strings.Contains(tidy.Combined(), "---"):
		out.checks = append(out.checks, feedback.Check{
			Name: "mod-tidy", Failed: true, Summary: "go.mod/go.sum are not tidy",
			Lines: firstLines(tidy.Combined(), 12),
			Fix:   "run `go mod tidy`",
		})
	case tidy.ExitCode != 0:
		out.notes = append(out.notes, "go mod tidy -diff failed: "+strings.Join(firstLines(tidy.Combined(), 2), " "))
	}

	// 5. govulncheck.
	if cfg.Vuln {
		if _, err := env.Runner.LookPath("govulncheck"); err != nil {
			out.notes = append(out.notes, "govulncheck not installed; vulnerability check skipped (go install golang.org/x/vuln/cmd/govulncheck@latest)")
		} else {
			out.ran = append(out.ran, "vuln")
			vuln, err := env.Runner.Run(ctx, root, "govulncheck", "./...")
			switch {
			case err != nil:
				out.notes = append(out.notes, "govulncheck could not run: "+err.Error())
			case vuln.ExitCode == 3:
				out.checks = append(out.checks, feedback.Check{
					Name: "vuln", Failed: true, Summary: "govulncheck found reachable vulnerabilities",
					Lines: vulnLines(vuln.Combined()),
					Fix:   "upgrade the affected modules (go get <module>@<fixed version>)",
				})
			case vuln.ExitCode != 0:
				out.notes = append(out.notes, "govulncheck failed: "+strings.Join(firstLines(vuln.Combined(), 2), " "))
			}
		}
	}
	return out
}

func (m *Module) testAndCover(ctx context.Context, env module.Env, root, modPath string, pkgs, files []string, in module.GateInput, out *moduleResult) {
	cfg := env.Config.Go
	tmp, err := os.CreateTemp("", "guardrails-cover-*.out")
	if err != nil {
		out.checks = append(out.checks, toolError("tests", err))
		return
	}
	profilePath := tmp.Name()
	_ = tmp.Close()
	defer func() { _ = os.Remove(profilePath) }()

	args := []string{"test", "-count=1", "-covermode=atomic", "-coverprofile=" + profilePath}
	if cfg.Race {
		args = append(args, "-race")
	}
	args = append(args, pkgs...)
	test, err := env.Runner.Run(ctx, root, "go", args...)
	if err != nil {
		out.checks = append(out.checks, toolError("tests", err))
		return
	}
	if test.ExitCode != 0 {
		out.checks = append(out.checks, testFailure(env.Repo, root, test.Combined()))
		return
	}

	f, err := os.Open(profilePath)
	if err != nil {
		out.checks = append(out.checks, toolError("coverage", err))
		return
	}
	prof, err := coverage.Parse(f)
	_ = f.Close()
	if err != nil {
		out.checks = append(out.checks, toolError("coverage", err))
		return
	}
	resolve := resolver(root, modPath)
	prof = prof.Filter(func(file string) bool {
		local, ok := resolve(file)
		if !ok {
			return true
		}
		return !excluded(root, local, cfg.Coverage.Exclude)
	})

	byPkg := prof.ByPackage()
	total := prof.Total()
	out.total, out.measured = total, true
	for pkg, s := range byPkg {
		out.coverage[pkg] = s.Percent()
	}
	if c, failed := coverageCheck(env, prof, byPkg, total, pkgs, in.Baseline, resolve); failed {
		out.checks = append(out.checks, c)
	}

	if in.BaseCommit != "" && cfg.PatchCoverage > 0 {
		changed := m.changedLines(ctx, env, in.BaseCommit, files)
		if len(changed) > 0 {
			out.ran = append(out.ran, "patch-coverage")
			if c, failed := patchCheck(env, prof.Patch(changed, resolve), prof, resolve, cfg.PatchCoverage); failed {
				out.checks = append(out.checks, c)
			}
		}
	}
}

func coverageCheck(env module.Env, prof *coverage.Profile, byPkg map[string]coverage.Stats, total coverage.Stats, pkgs []string, baseline map[string]float64, resolve coverage.Resolver) (feedback.Check, bool) {
	cfg := env.Config.Go.Coverage
	var problems []string
	pct := total.Percent()
	if pct+1e-9 < cfg.Total {
		problems = append(problems, fmt.Sprintf("%.1f%% < %.1f%%", pct, cfg.Total))
	}
	names := make([]string, 0, len(byPkg))
	for pkg := range byPkg {
		names = append(names, pkg)
	}
	sort.Strings(names)
	var lines []string
	for _, pkg := range names {
		p := byPkg[pkg].Percent()
		// With one package the floor line would repeat the total.
		if len(names) > 1 && p+1e-9 < cfg.PerPackageFloor {
			lines = append(lines, fmt.Sprintf("%s  %.1f%% below the per-package floor of %.0f%%", pkg, p, cfg.PerPackageFloor))
		}
		if cfg.Ratchet {
			if base, ok := baseline[pkg]; ok && p+ratchetTolerance < base {
				lines = append(lines, fmt.Sprintf("%s  %.1f%% dropped below its session baseline of %.1f%%", pkg, p, base))
			}
		}
	}
	if len(problems) == 0 && len(lines) == 0 {
		return feedback.Check{}, false
	}
	summary := strings.Join(problems, "; ")
	if summary == "" {
		summary = fmt.Sprintf("%.1f%% total", pct)
	}
	summary += " (touched: " + strings.Join(pkgs, ", ") + ")"
	for _, f := range coverage.WorstFuncs(prof.Funcs(resolve), 5) {
		lines = append(lines, fmt.Sprintf("%s:%d  %s  %d/%d stmts", displayPath(env.Repo, f.File), f.Line, f.Name, f.Covered, f.Total))
	}
	return feedback.Check{
		Name: "coverage", Failed: true, Summary: summary, Lines: lines,
		Fix: "add table-driven tests for the functions above",
	}, true
}

func patchCheck(env module.Env, pr coverage.PatchResult, prof *coverage.Profile, resolve coverage.Resolver, threshold float64) (feedback.Check, bool) {
	if pr.Total == 0 || pr.Percent()+1e-9 >= threshold {
		return feedback.Check{}, false
	}
	funcs := prof.Funcs(resolve)
	files := make([]string, 0, len(pr.Uncovered))
	for f := range pr.Uncovered {
		files = append(files, f)
	}
	sort.Strings(files)
	var lines []string
	for _, file := range files {
		groups := map[string][]int{}
		var order []string
		for _, l := range pr.Uncovered[file] {
			name := funcAt(funcs, file, l)
			if _, ok := groups[name]; !ok {
				order = append(order, name)
			}
			groups[name] = append(groups[name], l)
		}
		for _, name := range order {
			lines = append(lines, fmt.Sprintf("%s:%s  %s", displayPath(env.Repo, file), ranges(groups[name]), name))
		}
	}
	return feedback.Check{
		Name:    "patch-coverage",
		Failed:  true,
		Summary: fmt.Sprintf("%.1f%% of changed lines covered < %.1f%% (%d/%d)", pr.Percent(), threshold, pr.Covered, pr.Total),
		Lines:   lines,
		Fix:     "test the changed lines above; they are code you wrote this session",
	}, true
}

// changedLines returns lines changed since base for the touched non-test
// Go files, keyed by absolute path. Untracked files count as fully new.
func (m *Module) changedLines(ctx context.Context, env module.Env, base string, files []string) map[string]coverage.LineSet {
	out := map[string]coverage.LineSet{}
	var targets []string
	for _, f := range files {
		if strings.HasSuffix(f, ".go") && !strings.HasSuffix(f, "_test.go") {
			targets = append(targets, f)
		}
	}
	if len(targets) == 0 {
		return out
	}
	args := append([]string{"diff", "--unified=0", "--no-color", "--no-ext-diff", "--src-prefix=a/", "--dst-prefix=b/", base, "--"}, targets...)
	res, err := env.Runner.Run(ctx, env.Repo, "git", args...)
	if err != nil || res.ExitCode != 0 {
		return out
	}
	for rel, set := range diff.ParseUnified(res.Stdout) {
		out[filepath.Join(env.Repo, filepath.FromSlash(rel))] = set
	}
	untracked, err := env.Runner.Run(ctx, env.Repo, "git", append([]string{"ls-files", "--others", "--exclude-standard", "--"}, targets...)...)
	if err == nil && untracked.ExitCode == 0 {
		for _, rel := range strings.Fields(untracked.Stdout) {
			abs := filepath.Join(env.Repo, filepath.FromSlash(rel))
			out[abs] = allLines(abs)
		}
	}
	return out
}

func allLines(path string) coverage.LineSet {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	set := coverage.LineSet{}
	for i := range strings.Split(string(data), "\n") {
		set[i+1] = true
	}
	return set
}

func resolver(root, modPath string) coverage.Resolver {
	return func(file string) (string, bool) {
		rel, ok := strings.CutPrefix(file, modPath+"/")
		if !ok {
			return "", false
		}
		return filepath.Join(root, filepath.FromSlash(rel)), true
	}
}

func excluded(root, local string, patterns []string) bool {
	rel, err := filepath.Rel(root, local)
	if err != nil {
		return false
	}
	if glob.Any(patterns, filepath.ToSlash(rel)) {
		return true
	}
	data, err := os.ReadFile(local)
	return err == nil && isGenerated(data)
}

var failRe = regexp.MustCompile(`^\s*--- FAIL: (\S+)`)

func testFailure(repo, root, out string) feedback.Check {
	var failed []string
	for _, l := range strings.Split(out, "\n") {
		if m := failRe.FindStringSubmatch(l); m != nil {
			failed = append(failed, m[1])
		}
	}
	issues := relIssues(repo, root, module.ParseLocations(out, "test"))
	summary := "go test failed"
	if len(failed) > 0 {
		summary = fmt.Sprintf("%d failing test%s: %s", len(failed), plural(len(failed)), strings.Join(capList(failed, 8), ", "))
	}
	c := feedback.Check{
		Name: "tests", Failed: true, Summary: summary, Issues: issues,
		Fix: "make the failing tests pass; do not skip or delete them",
	}
	if len(issues) == 0 {
		c.Lines = lastLines(out, 15)
	}
	return c
}

func vulnLines(out string) []string {
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "Vulnerability #") || strings.HasPrefix(t, "Found in:") || strings.HasPrefix(t, "Fixed in:") {
			lines = append(lines, t)
		}
	}
	if len(lines) == 0 {
		return lastLines(out, 10)
	}
	return lines
}

func funcAt(funcs []coverage.Func, file string, line int) string {
	best := "(top level)"
	for _, f := range funcs {
		if f.File == file && f.Line <= line {
			best = f.Name
		}
	}
	return best
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

func displayPath(repo, p string) string {
	if rel, err := filepath.Rel(repo, p); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(p)
}

func linesIfNoIssues(out string) []string {
	if len(module.ParseLocations(out, "")) > 0 {
		return nil
	}
	return firstLines(out, 15)
}

func lastLines(s string, n int) []string {
	all := firstLines(s, 1<<30)
	if len(all) > n {
		all = all[len(all)-n:]
	}
	return all
}

func capList(items []string, n int) []string {
	if len(items) <= n {
		return items
	}
	return append(items[:n:n], fmt.Sprintf("+%d more", len(items)-n))
}

// allGoFiles lists every .go file under repo, skipping vendor, testdata and
// hidden directories.
func allGoFiles(repo string) []string {
	var out []string
	_ = filepath.WalkDir(repo, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			// Unreadable entries are skipped, not fatal.
			return skipUnreadable(d)
		}
		if d.IsDir() {
			name := d.Name()
			if p != repo && (strings.HasPrefix(name, ".") || name == "vendor" || name == "testdata" || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(p, ".go") {
			out = append(out, p)
		}
		return nil
	})
	return out
}

// skipUnreadable skips an unreadable directory and ignores an unreadable
// file, so one bad entry never stops the walk.
func skipUnreadable(d os.DirEntry) error {
	if d != nil && d.IsDir() {
		return filepath.SkipDir
	}
	return nil
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
