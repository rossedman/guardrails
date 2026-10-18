// Package python is the Python language module: ruff format and ruff
// --fix on edit, ruff and a type checker on the edited file, and a Stop gate
// of ruff, pytest with coverage (total, ratchet and patch), the type checker
// and `uv lock --check`.
package python

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rossedman/guardrails/internal/config"
	"github.com/rossedman/guardrails/internal/feedback"
	"github.com/rossedman/guardrails/internal/module"
	"github.com/rossedman/guardrails/internal/runner"
	"github.com/rossedman/guardrails/internal/state"
	"github.com/rossedman/guardrails/internal/tools"
)

// Module implements module.Module for Python.
type Module struct{}

// New returns the Python module.
func New() *Module { return &Module{} }

// Name implements module.Module.
func (*Module) Name() string { return "python" }

// Enabled implements module.Module.
func (*Module) Enabled(cfg config.Config) bool { return cfg.PythonEnabled() }

// Match implements module.Module.
func (*Module) Match(path string) bool {
	base := filepath.Base(path)
	return strings.HasSuffix(base, ".py") || base == "pyproject.toml"
}

// Tools implements module.Module.
func (*Module) Tools() []module.Tool {
	return []module.Tool{
		{Name: "ruff", Required: true, Install: "managed, or uv tool install ruff", VersionArgs: []string{"--version"}},
		{Name: "mypy", Required: true, Install: "managed, or uv tool install mypy", VersionArgs: []string{"--version"}},
		{Name: "uv", Required: true, Install: "managed, or https://docs.astral.sh/uv/", VersionArgs: []string{"--version"}},
	}
}

// Standards implements module.Module.
func (*Module) Standards(cfg config.Config) string {
	p := cfg.Python
	var b strings.Builder
	b.WriteString("Python standards: ruff format and ruff --fix are applied automatically after each edit; ruff")
	switch p.Typecheck {
	case "mypy":
		b.WriteString(" and mypy")
		if p.Strict {
			b.WriteString(" --strict")
		}
	case "pyright":
		b.WriteString(" and pyright")
	}
	b.WriteString(" check the edited file. ")
	fmt.Fprintf(&b, "Before a turn can end, pytest must pass with at least %.0f%% coverage", p.Coverage.Total)
	if cfg.Python.Coverage.Ratchet {
		b.WriteString(" (no package below its starting coverage)")
	}
	if p.PatchCoverage > 0 {
		fmt.Fprintf(&b, ", %.0f%% of Python lines changed this session must be covered", p.PatchCoverage)
	}
	b.WriteString(", touched files must be ruff and type-check clean, and uv.lock must be current.")
	return b.String()
}

// tool returns the named entry of Tools.
func (m *Module) tool(name string) module.Tool {
	for _, t := range m.Tools() {
		if t.Name == name {
			return t
		}
	}
	return module.Tool{Name: name}
}

// Fix implements module.Module.
func (m *Module) Fix(ctx context.Context, env module.Env, files []string) module.FixResult {
	var res module.FixResult
	for _, f := range pyFiles(files) {
		proj := projectRoot(env.Repo, f)
		ruff, err := resolve(ctx, env, proj, "ruff")
		if err != nil {
			if module.IsUnavailable(err) {
				res.Notes = append(res.Notes, module.Unavailable(m.tool("ruff"), err)+"; Python files were not formatted")
			}
			return res
		}
		_, _ = run(ctx, env, proj, ruff, "format", "--quiet", f)
		_, _ = run(ctx, env, proj, ruff, "check", "--fix", "--quiet", f)
	}
	return res
}

// Check implements module.Module: ruff and the type checker on the edited
// files, reporting issues in those files only. Unavailable tools are
// skipped; the gate reports them.
func (m *Module) Check(ctx context.Context, env module.Env, files []string) []feedback.Check {
	var checks []feedback.Check
	for proj, pf := range byProject(env.Repo, pyFiles(files)) {
		want := fileSet(pf)
		if c, ok := m.ruffCheck(ctx, env, proj, pf, want); ok {
			checks = append(checks, c)
		}
		if c, ok, _ := m.typecheck(ctx, env, proj, pf, want); ok {
			checks = append(checks, c)
		}
	}
	sort.Slice(checks, func(i, j int) bool { return checks[i].Name < checks[j].Name })
	return checks
}

// ruffCheck runs `ruff check` and returns a failing check for issues in
// want. An unavailable ruff yields no check.
func (m *Module) ruffCheck(ctx context.Context, env module.Env, proj string, files []string, want map[string]bool) (feedback.Check, bool) {
	ruff, err := resolve(ctx, env, proj, "ruff")
	if err != nil {
		if module.IsUnavailable(err) {
			return feedback.Check{}, false
		}
		return toolError("ruff", err), true
	}
	res, err := run(ctx, env, proj, ruff, append([]string{"check", "--output-format=json", "--no-fix"}, files...)...)
	if err != nil {
		return toolError("ruff", err), true
	}
	issues, perr := parseRuff(res.Stdout)
	if perr != nil {
		if res.ExitCode == 0 {
			return feedback.Check{}, false
		}
		return feedback.Check{Name: "ruff", Failed: true, Summary: "ruff failed to run", Lines: firstLines(res.Combined(), 10)}, true
	}
	issues = only(relIssues(env.Repo, proj, issues), env.Repo, want)
	if len(issues) == 0 {
		return feedback.Check{}, false
	}
	return feedback.Check{
		Name: "ruff", Failed: true,
		Summary: fmt.Sprintf("%d issue%s", len(issues), plural(len(issues))),
		Issues:  issues,
		Fix:     "fix each issue; `# noqa` is not allowed",
	}, true
}

// typecheck runs the configured type checker over targets (files or
// package directories). It returns the failing check, whether there is
// one, and an unavailability error for the gate to report.
func (m *Module) typecheck(ctx context.Context, env module.Env, proj string, targets []string, want map[string]bool) (feedback.Check, bool, error) {
	var (
		cmd     tools.Command
		err     error
		checker = env.Config.Python.Typecheck
	)
	switch checker {
	case "mypy":
		cmd, err = resolve(ctx, env, proj, "mypy")
	case "pyright":
		if _, lerr := env.Runner.LookPath("pyright"); lerr != nil {
			err = fmt.Errorf("pyright: %w", tools.ErrMissing)
		}
		cmd = tools.Command{Path: "pyright"}
	default:
		return feedback.Check{}, false, nil
	}
	if err != nil {
		if module.IsUnavailable(err) {
			return feedback.Check{}, false, err
		}
		return toolError(checker, err), true, nil
	}

	var (
		res    runner.Result
		issues []feedback.Issue
	)
	if checker == "mypy" {
		args := []string{
			"--show-column-numbers", "--show-error-codes", "--no-error-summary",
			"--no-color-output", "--no-pretty", "--hide-error-context",
		}
		if env.Config.Python.Strict {
			args = append(args, "--strict")
		}
		if py := venvPython(env.Repo, proj); py != "" {
			args = append(args, "--python-executable", py)
		}
		if env.DataDir != "" {
			args = append(args, "--cache-dir", filepath.Join(env.DataDir, "mypy-cache", state.Key(proj)))
		}
		res, err = run(ctx, env, proj, cmd, append(args, targets...)...)
		if err != nil {
			return toolError(checker, err), true, nil
		}
		issues = parseMypy(res.Combined())
		if res.ExitCode > 1 && len(issues) == 0 {
			return feedback.Check{Name: checker, Failed: true, Summary: "mypy failed to run", Lines: firstLines(res.Combined(), 10)}, true, nil
		}
	} else {
		res, err = run(ctx, env, proj, cmd, append([]string{"--outputjson"}, targets...)...)
		if err != nil {
			return toolError(checker, err), true, nil
		}
		var perr error
		issues, perr = parsePyright(res.Stdout)
		if perr != nil {
			if res.ExitCode == 0 {
				return feedback.Check{}, false, nil
			}
			return feedback.Check{Name: checker, Failed: true, Summary: "pyright failed to run", Lines: firstLines(res.Combined(), 10)}, true, nil
		}
	}
	issues = relIssues(env.Repo, proj, issues)
	if want != nil {
		issues = only(issues, env.Repo, want)
	}
	if len(issues) == 0 {
		return feedback.Check{}, false, nil
	}
	return feedback.Check{
		Name: checker, Failed: true,
		Summary: fmt.Sprintf("%d type error%s", len(issues), plural(len(issues))),
		Issues:  issues,
		Fix:     "fix the types; `# type: ignore` is not allowed",
	}, true, nil
}

// resolve prefers a project-local executable (.venv/bin/<name> in the
// project or the repo) over the managed or PATH copy.
func resolve(ctx context.Context, env module.Env, proj, name string) (tools.Command, error) {
	for _, dir := range uniq(proj, env.Repo) {
		p := filepath.Join(dir, ".venv", "bin", name)
		if isExecutable(p) {
			return tools.Command{Path: p}, nil
		}
	}
	return env.Tool(ctx, name)
}

func run(ctx context.Context, env module.Env, dir string, cmd tools.Command, args ...string) (runner.Result, error) {
	return env.Runner.Run(ctx, dir, cmd.Path, append(append([]string(nil), cmd.Args...), args...)...)
}

// venvPython returns the project's virtualenv interpreter, if any.
func venvPython(repo, proj string) string {
	for _, dir := range uniq(proj, repo) {
		p := filepath.Join(dir, ".venv", "bin", "python")
		if isExecutable(p) {
			return p
		}
	}
	return ""
}

func isExecutable(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}

func uniq(dirs ...string) []string {
	var out []string
	seen := map[string]bool{}
	for _, d := range dirs {
		if d != "" && !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out
}

// projectRoot returns the nearest directory at or above file's directory
// holding pyproject.toml, setup.py or setup.cfg, stopping at repo. Without
// one the repo is the project.
func projectRoot(repo, file string) string {
	dir := filepath.Dir(file)
	for {
		for _, marker := range []string{"pyproject.toml", "setup.py", "setup.cfg"} {
			if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
				return dir
			}
		}
		if dir == repo || !within(repo, dir) {
			return repo
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return repo
		}
		dir = parent
	}
}

func within(repo, p string) bool {
	rel, err := filepath.Rel(repo, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// byProject groups files by project root.
func byProject(repo string, files []string) map[string][]string {
	out := map[string][]string{}
	for _, f := range files {
		p := projectRoot(repo, f)
		out[p] = append(out[p], f)
	}
	return out
}

// pyFiles keeps existing .py files.
func pyFiles(files []string) []string {
	var out []string
	for _, f := range files {
		if !strings.HasSuffix(f, ".py") {
			continue
		}
		if _, err := os.Stat(f); err != nil {
			continue
		}
		out = append(out, f)
	}
	return out
}

func isTestFile(p string) bool {
	base := filepath.Base(p)
	return strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py") || base == "conftest.py"
}

func fileSet(files []string) map[string]bool {
	m := map[string]bool{}
	for _, f := range files {
		m[filepath.Clean(f)] = true
	}
	return m
}

// relIssues rewrites issue paths (relative to proj, or absolute) to be
// relative to the repo for display.
func relIssues(repo, proj string, issues []feedback.Issue) []feedback.Issue {
	for i := range issues {
		issues[i].File = displayPath(repo, absTo(proj, issues[i].File))
	}
	return issues
}

func absTo(dir, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(dir, p)
}

func only(issues []feedback.Issue, repo string, want map[string]bool) []feedback.Issue {
	var out []feedback.Issue
	for _, is := range issues {
		if want[filepath.Join(repo, filepath.FromSlash(is.File))] {
			out = append(out, is)
		}
	}
	return out
}

func displayPath(repo, p string) string {
	if rel, err := filepath.Rel(repo, p); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(p)
}

func toolError(name string, err error) feedback.Check {
	return feedback.Check{Name: name, Failed: true, Summary: "could not run: " + err.Error()}
}

// unavailableCheck is the gate's failing check for a required tool that
// cannot be resolved.
func (m *Module) unavailableCheck(name string, err error) feedback.Check {
	return feedback.Check{
		Name: "tools", Failed: true, Summary: module.Unavailable(m.tool(name), err),
		Fix: "the human must install " + name + " or set tools.install: auto",
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func firstLines(s string, n int) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		out = append(out, l)
		if len(out) == n {
			break
		}
	}
	return out
}

func lastLines(s string, n int) []string {
	all := firstLines(s, 1<<30)
	if len(all) > n {
		all = all[len(all)-n:]
	}
	return all
}

var errNoEnv = errors.New("no Python environment: install uv (or set tools.install: auto) or create a .venv")
