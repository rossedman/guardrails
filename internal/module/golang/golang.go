// Package golang is the Go language module: gofumpt and goimports on edit,
// go vet and golangci-lint per package, and a Stop gate of build, race
// tests, coverage (total, per-package floor, ratchet, patch), full lint,
// go mod tidy and govulncheck.
package golang

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rossedman/guardrails/internal/config"
	"github.com/rossedman/guardrails/internal/feedback"
	"github.com/rossedman/guardrails/internal/module"
	"github.com/rossedman/guardrails/internal/tools"
)

// BundledLintConfig is used when a repo has no golangci-lint config.
//
//go:embed golangci.yml
var BundledLintConfig []byte

// Module implements module.Module for Go.
type Module struct{}

// New returns the Go module.
func New() *Module { return &Module{} }

// Name implements module.Module.
func (*Module) Name() string { return "go" }

// Enabled implements module.Module.
func (*Module) Enabled(cfg config.Config) bool { return cfg.GoEnabled() }

// Match implements module.Module.
func (*Module) Match(path string) bool {
	base := filepath.Base(path)
	return strings.HasSuffix(base, ".go") || base == "go.mod"
}

// Tools implements module.Module.
func (*Module) Tools() []module.Tool {
	return []module.Tool{
		{Name: "go", Required: true, Install: "https://go.dev/doc/install", VersionArgs: []string{"version"}},
		{Name: "golangci-lint", Required: true, Install: "managed, or https://golangci-lint.run/welcome/install/", VersionArgs: []string{"version"}},
		{Name: "gofumpt", Install: "managed, or go install mvdan.cc/gofumpt@latest", VersionArgs: []string{"--version"}},
		{Name: "goimports", Install: "managed, or go install golang.org/x/tools/cmd/goimports@latest"},
		{Name: "govulncheck", Install: "managed, or go install golang.org/x/vuln/cmd/govulncheck@latest", VersionArgs: []string{"-version"}},
	}
}

// Standards implements module.Module.
func (*Module) Standards(cfg config.Config) string {
	var b strings.Builder
	g := cfg.Go
	b.WriteString("Go standards: gofumpt/goimports formatting is applied automatically after each edit; go vet and golangci-lint run on the edited package. ")
	fmt.Fprintf(&b, "Before a turn can end, touched packages must build, pass `go test -race`, reach %.0f%% total statement coverage (no package below %.0f%%", g.Coverage.Total, g.Coverage.PerPackageFloor)
	if g.Coverage.Ratchet {
		b.WriteString(", none below its starting coverage")
	}
	b.WriteString(")")
	if g.PatchCoverage > 0 {
		fmt.Fprintf(&b, ", cover %.0f%% of lines changed this session", g.PatchCoverage)
	}
	b.WriteString(", be golangci-lint clean and keep go.mod tidy")
	if g.Vuln {
		b.WriteString(", with no reachable govulncheck findings")
	}
	b.WriteString(".")
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

// Fix implements module.Module. goimports runs first, then gofumpt (or
// gofmt when gofumpt is missing), so the stricter formatter has the final
// say.
func (m *Module) Fix(ctx context.Context, env module.Env, files []string) module.FixResult {
	var res module.FixResult
	goFiles := editableGoFiles(files)
	if len(goFiles) == 0 {
		return res
	}
	if _, err := env.RunTool(ctx, env.Repo, "goimports", append([]string{"-w"}, goFiles...)...); module.IsUnavailable(err) {
		res.Notes = append(res.Notes, module.Unavailable(m.tool("goimports"), err)+"; imports were not fixed")
	}
	if _, err := env.RunTool(ctx, env.Repo, "gofumpt", append([]string{"-w"}, goFiles...)...); module.IsUnavailable(err) {
		_, _ = env.Runner.Run(ctx, env.Repo, "gofmt", append([]string{"-w"}, goFiles...)...)
		res.Notes = append(res.Notes, module.Unavailable(m.tool("gofumpt"), err)+"; fell back to gofmt")
	}
	return res
}

// Check implements module.Module: go vet plus golangci-lint on the edited
// files' packages, reporting issues in those files only.
func (m *Module) Check(ctx context.Context, env module.Env, files []string) []feedback.Check {
	var checks []feedback.Check
	for root, pkgFiles := range byModule(files) {
		pkgs := packagesOf(root, pkgFiles)
		if len(pkgs) == 0 {
			continue
		}
		want := fileSet(pkgFiles)

		vet, err := env.Runner.Run(ctx, root, "go", append([]string{"vet"}, pkgs...)...)
		if err != nil {
			checks = append(checks, toolError("vet", err))
		} else if vet.ExitCode != 0 {
			issues := relIssues(env.Repo, root, module.ParseLocations(vet.Combined(), "vet"))
			// Type errors anywhere in the package matter: the edit may have
			// broken another file.
			if !typecheckFailure(vet.Combined()) {
				issues = only(issues, env.Repo, want)
			}
			if len(issues) > 0 {
				checks = append(checks, feedback.Check{
					Name: "vet", Failed: true,
					Summary: fmt.Sprintf("%d issue%s", len(issues), plural(len(issues))),
					Issues:  issues,
					Fix:     "fix the reported problems; the build must type-check",
				})
			}
		}

		if env.Config.Go.Lint.OnEdit == "off" {
			continue
		}
		args := []string{"run"}
		if env.Config.Go.Lint.OnEdit == "fast" {
			args = append(args, "--fast-only")
		}
		if c, ok := m.lint(ctx, env, root, append(args, pkgs...), want); ok {
			checks = append(checks, c)
		}
	}
	return checks
}

// lint runs golangci-lint and returns a failing check when there are
// issues in want (or in any file when want is nil).
func (m *Module) lint(ctx context.Context, env module.Env, root string, args []string, want map[string]bool) (feedback.Check, bool) {
	if _, err := env.Tool(ctx, "golangci-lint"); err != nil {
		if errors.Is(err, tools.ErrInstalling) {
			return feedback.Check{}, false
		}
		return feedback.Check{
			Name: "lint", Failed: true, Summary: module.Unavailable(m.tool("golangci-lint"), err),
			Fix: "the human must install golangci-lint v2 or set tools.install: auto",
		}, true
	}
	full := []string{args[0], "--output.json.path=stdout", "--output.text.path=stderr", "--show-stats=false"}
	if cfgPath := m.lintConfig(env, root); cfgPath != "" {
		full = append(full, "--config="+cfgPath)
	}
	full = append(full, args[1:]...)
	res, err := env.RunTool(ctx, root, "golangci-lint", full...)
	if err != nil {
		return toolError("lint", err), true
	}
	issues, perr := parseLint(res.Stdout)
	if perr != nil {
		if res.ExitCode == 0 {
			return feedback.Check{}, false
		}
		return feedback.Check{
			Name: "lint", Failed: true, Summary: "golangci-lint failed to run",
			Lines: firstLines(res.Combined(), 15),
		}, true
	}
	issues = relIssues(env.Repo, root, issues)
	if want != nil {
		issues = only(issues, env.Repo, want)
	}
	if len(issues) == 0 {
		return feedback.Check{}, false
	}
	return feedback.Check{
		Name: "lint", Failed: true,
		Summary: fmt.Sprintf("%d issue%s", len(issues), plural(len(issues))),
		Issues:  issues,
		Fix:     "fix each issue; suppression comments are not allowed",
	}, true
}

// lintConfig returns the config flag value: empty when the repo has its
// own config (golangci-lint finds it), else the bundled config written to
// the data dir.
func (m *Module) lintConfig(env module.Env, root string) string {
	if c := env.Config.Go.Lint.Config; c != "" {
		p := c
		if !filepath.IsAbs(p) {
			p = filepath.Join(env.Repo, c)
		}
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	for _, name := range []string{".golangci.yml", ".golangci.yaml", ".golangci.toml", ".golangci.json"} {
		if _, err := os.Stat(filepath.Join(root, name)); err == nil {
			return ""
		}
	}
	if env.DataDir == "" {
		return ""
	}
	p := filepath.Join(env.DataDir, "golangci.yml")
	if err := os.MkdirAll(env.DataDir, 0o700); err != nil {
		return ""
	}
	if err := os.WriteFile(p, BundledLintConfig, 0o600); err != nil {
		return ""
	}
	return p
}

type lintReport struct {
	Issues []struct {
		FromLinter string `json:"FromLinter"`
		Text       string `json:"Text"`
		Pos        struct {
			Filename string `json:"Filename"`
			Line     int    `json:"Line"`
			Column   int    `json:"Column"`
		} `json:"Pos"`
	} `json:"Issues"`
}

// parseLint reads golangci-lint JSON output. The JSON report is the first
// line that starts with '{'.
func parseLint(out string) ([]feedback.Issue, error) {
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var rep lintReport
		if err := json.Unmarshal([]byte(line), &rep); err != nil {
			return nil, fmt.Errorf("parse golangci-lint output: %w", err)
		}
		issues := make([]feedback.Issue, 0, len(rep.Issues))
		for _, is := range rep.Issues {
			issues = append(issues, feedback.Issue{
				File: is.Pos.Filename, Line: is.Pos.Line, Col: is.Pos.Column,
				Rule: is.FromLinter, Message: is.Text,
			})
		}
		return issues, nil
	}
	return nil, errors.New("no JSON report in golangci-lint output")
}

// editableGoFiles keeps existing, non-generated .go files.
func editableGoFiles(files []string) []string {
	var out []string
	for _, f := range files {
		if !strings.HasSuffix(f, ".go") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil || isGenerated(data) {
			continue
		}
		out = append(out, f)
	}
	return out
}

func isGenerated(data []byte) bool {
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	for i := 0; i < 20 && sc.Scan(); i++ {
		l := sc.Text()
		if strings.HasPrefix(l, "// Code generated ") && strings.HasSuffix(strings.TrimSuffix(l, "."), "DO NOT EDIT") {
			return true
		}
	}
	return false
}

// FindModuleRoot returns the directory of the nearest go.mod at or above
// dir, or "" if there is none.
func FindModuleRoot(dir string) string {
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// ModulePath reads the module path from root/go.mod.
func ModulePath(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", err
	}
	for _, l := range strings.Split(string(data), "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "module") {
			f := strings.Fields(l)
			if len(f) >= 2 {
				return strings.Trim(f[1], `"`), nil
			}
		}
	}
	return "", fmt.Errorf("no module directive in %s", filepath.Join(root, "go.mod"))
}

// byModule groups files by module root, dropping files outside any module.
func byModule(files []string) map[string][]string {
	out := map[string][]string{}
	for _, f := range files {
		root := FindModuleRoot(filepath.Dir(f))
		if root == "" {
			continue
		}
		out[root] = append(out[root], f)
	}
	return out
}

// packagesOf returns ./relative package patterns for the directories of
// files that still contain Go files.
func packagesOf(root string, files []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range files {
		dir := filepath.Dir(f)
		if filepath.Base(f) == "go.mod" || seen[dir] || !hasGoFiles(dir) {
			continue
		}
		seen[dir] = true
		rel, err := filepath.Rel(root, dir)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		out = append(out, "./"+filepath.ToSlash(rel))
	}
	sort.Strings(out)
	return out
}

func hasGoFiles(dir string) bool {
	matches, _ := filepath.Glob(filepath.Join(dir, "*.go"))
	return len(matches) > 0
}

func fileSet(files []string) map[string]bool {
	m := map[string]bool{}
	for _, f := range files {
		m[filepath.Clean(f)] = true
	}
	return m
}

// relIssues rewrites issue paths (relative to root, or absolute) to be
// relative to the repo for display.
func relIssues(repo, root string, issues []feedback.Issue) []feedback.Issue {
	for i := range issues {
		p := issues[i].File
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, p)
		}
		if rel, err := filepath.Rel(repo, p); err == nil && !strings.HasPrefix(rel, "..") {
			p = rel
		}
		issues[i].File = filepath.ToSlash(p)
	}
	return issues
}

// only keeps issues whose (repo-relative) file is in want.
func only(issues []feedback.Issue, repo string, want map[string]bool) []feedback.Issue {
	var out []feedback.Issue
	for _, is := range issues {
		if want[filepath.Join(repo, filepath.FromSlash(is.File))] {
			out = append(out, is)
		}
	}
	return out
}

func typecheckFailure(out string) bool {
	return strings.Contains(out, "vet: ") || strings.Contains(out, "undefined: ") ||
		strings.Contains(out, "cannot use ") || strings.Contains(out, "syntax error")
}

func toolError(name string, err error) feedback.Check {
	return feedback.Check{Name: name, Failed: true, Summary: "could not run: " + err.Error()}
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
