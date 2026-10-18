// Package terraform is the Terraform module: `terraform fmt` on edit, tflint
// on the edited module, and a Stop gate of tflint, `init -backend=false` +
// `validate`, a trivy config scan and provider version constraints.
//
// Every Terraform command runs with TF_DATA_DIR and TF_PLUGIN_CACHE_DIR
// under the plugin data directory, so the gate never writes .terraform/
// into the repository. The runner has no environment support, so commands
// are wrapped in the POSIX `env` program rather than widening the Runner
// interface for one module.
package terraform

import (
	"context"
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
	"github.com/rossedman/guardrails/internal/state"
	"github.com/rossedman/guardrails/internal/tools"
)

// Module implements module.Module for Terraform.
type Module struct{}

// New returns the Terraform module.
func New() *Module { return &Module{} }

// Name implements module.Module.
func (*Module) Name() string { return "terraform" }

// Enabled implements module.Module.
func (*Module) Enabled(cfg config.Config) bool { return cfg.TerraformEnabled() }

// Match implements module.Module.
func (*Module) Match(path string) bool {
	if strings.Contains(filepath.ToSlash(path), "/.terraform/") {
		return false
	}
	return strings.HasSuffix(path, ".tf") || strings.HasSuffix(path, ".tfvars")
}

// Tools implements module.Module. OpenTofu users resolve tofu at gate
// time; it is not listed so a Terraform repo never downloads it.
func (*Module) Tools() []module.Tool {
	return []module.Tool{
		{Name: "terraform", Required: true, Install: "managed, or https://developer.hashicorp.com/terraform/install", VersionArgs: []string{"version"}},
		{Name: "tflint", Required: true, Install: "managed, or https://github.com/terraform-linters/tflint#installation", VersionArgs: []string{"--version"}},
		{Name: "trivy", Required: true, Install: "managed, or https://trivy.dev/latest/getting-started/installation/", VersionArgs: []string{"--version"}},
	}
}

func (m *Module) tool(name string) module.Tool {
	for _, t := range m.Tools() {
		if t.Name == name {
			return t
		}
	}
	return module.Tool{Name: name, Required: true, Install: "managed, or https://opentofu.org/docs/intro/install/"}
}

// Standards implements module.Module.
func (*Module) Standards(cfg config.Config) string {
	t := cfg.Terraform
	var b strings.Builder
	fmt.Fprintf(&b, "Terraform standards: `%s fmt` is applied automatically after each edit", t.Binary)
	if t.Tflint {
		b.WriteString(" and tflint runs on the edited module")
	}
	fmt.Fprintf(&b, ". Before a turn can end, touched modules must pass `%s init -backend=false` and `validate`", t.Binary)
	if t.Tflint {
		b.WriteString(", be tflint clean")
	}
	if t.Scan == "trivy" {
		b.WriteString(", have no HIGH or CRITICAL trivy misconfigurations")
	}
	if t.RequireVersionConstraints {
		b.WriteString(", and declare a version constraint for every provider in required_providers")
	}
	b.WriteString(". Plan only: apply, destroy and state changes are the human's.")
	return b.String()
}

// Fix implements module.Module.
func (m *Module) Fix(ctx context.Context, env module.Env, files []string) module.FixResult {
	var res module.FixResult
	bin := env.Config.Terraform.Binary
	for _, f := range files {
		if _, err := os.Stat(f); err != nil {
			continue
		}
		if _, err := env.RunTool(ctx, filepath.Dir(f), bin, "fmt", "-no-color", f); module.IsUnavailable(err) {
			res.Notes = append(res.Notes, module.Unavailable(m.tool(bin), err)+"; Terraform files were not formatted")
			return res
		}
	}
	return res
}

// Check implements module.Module: tflint on each edited file's module,
// reporting issues in the edited files only. Plugins are never initialized
// here; if tflint needs `--init`, the check waits for the gate.
func (m *Module) Check(ctx context.Context, env module.Env, files []string) []feedback.Check {
	if !env.Config.Terraform.Tflint {
		return nil
	}
	if _, err := env.Tool(ctx, "tflint"); err != nil {
		return nil
	}
	var issues []feedback.Issue
	for _, dir := range moduleDirs(files) {
		found, ok := m.tflint(ctx, env, dir)
		if !ok {
			continue
		}
		issues = append(issues, only(found, env.Repo, files)...)
	}
	if len(issues) == 0 {
		return nil
	}
	return []feedback.Check{lintCheck(issues)}
}

func lintCheck(issues []feedback.Issue) feedback.Check {
	return feedback.Check{
		Name: "tflint", Failed: true,
		Summary: fmt.Sprintf("%d issue%s", len(issues), plural(len(issues))),
		Issues:  issues,
		Fix:     "fix each issue; tflint-ignore comments are not allowed",
	}
}

type tflintReport struct {
	Issues []struct {
		Rule struct {
			Name     string `json:"name"`
			Severity string `json:"severity"`
		} `json:"rule"`
		Message string `json:"message"`
		Range   struct {
			Filename string `json:"filename"`
			Start    struct {
				Line   int `json:"line"`
				Column int `json:"column"`
			} `json:"start"`
		} `json:"range"`
	} `json:"issues"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// tflint runs tflint in dir. ok is false when tflint could not produce a
// report (plugins not initialized, crashes); those are never blocking at
// edit time.
func (m *Module) tflint(ctx context.Context, env module.Env, dir string) ([]feedback.Issue, bool) {
	res, err := env.RunTool(ctx, dir, "tflint", "--chdir="+dir, "--format=json", "--no-color")
	if err != nil {
		return nil, false
	}
	var rep tflintReport
	if jerr := json.Unmarshal([]byte(strings.TrimSpace(res.Stdout)), &rep); jerr != nil {
		return nil, false
	}
	if len(rep.Errors) > 0 && len(rep.Issues) == 0 {
		return nil, false
	}
	var issues []feedback.Issue
	for _, is := range rep.Issues {
		if is.Rule.Severity == "notice" {
			continue
		}
		issues = append(issues, feedback.Issue{
			File: display(env.Repo, filepath.Join(dir, is.Range.Filename)),
			Line: is.Range.Start.Line, Col: is.Range.Start.Column,
			Rule: is.Rule.Name, Message: is.Message,
		})
	}
	return issues, true
}

// Gate implements module.Module.
func (m *Module) Gate(ctx context.Context, env module.Env, in module.GateInput) module.GateResult {
	res := module.GateResult{Total: -1}
	cfg := env.Config.Terraform
	files := in.Files
	if in.All {
		files = allTerraformFiles(env.Repo)
	}
	dirs := moduleDirs(files)
	if len(dirs) == 0 {
		return res
	}

	needed := []string{cfg.Binary}
	if cfg.Tflint {
		needed = append(needed, "tflint")
	}
	if cfg.Scan == "trivy" {
		needed = append(needed, "trivy")
	}
	available := map[string]bool{}
	var missing []string
	for _, name := range needed {
		if _, err := env.Tool(ctx, name); err != nil {
			missing = append(missing, module.Unavailable(m.tool(name), err))
			continue
		}
		available[name] = true
	}
	if len(missing) > 0 {
		res.Checks = append(res.Checks, feedback.Check{
			Name: "tools", Failed: true, Summary: strings.Join(missing, "; "),
			Fix: "the human must install the missing tools or set tools.install: auto",
		})
	}

	var (
		lintIssues, validateIssues, scanIssues, versionIssues []feedback.Issue
		initLines                                             []string
	)
	for _, dir := range dirs {
		if available["tflint"] {
			res.Ran = appendUnique(res.Ran, "tflint")
			if r, err := env.RunTool(ctx, dir, "tflint", "--chdir="+dir, "--init", "--no-color"); err != nil || r.ExitCode != 0 {
				res.Notes = append(res.Notes, "tflint --init failed in "+display(env.Repo, dir)+"; plugin rules may be missing")
			}
			if found, ok := m.tflint(ctx, env, dir); ok {
				lintIssues = append(lintIssues, scope(found, env.Repo, files, in.All)...)
			} else {
				res.Notes = append(res.Notes, "tflint could not lint "+display(env.Repo, dir))
			}
		}
		if available[cfg.Binary] {
			res.Ran = appendUnique(res.Ran, "init", "validate")
			v, lines, note := m.validate(ctx, env, dir)
			validateIssues = append(validateIssues, v...)
			initLines = append(initLines, lines...)
			if note != "" {
				res.Notes = append(res.Notes, note)
			}
		}
		if available["trivy"] {
			res.Ran = appendUnique(res.Ran, "trivy")
			found, note := m.trivy(ctx, env, dir)
			scanIssues = append(scanIssues, scope(found, env.Repo, files, in.All)...)
			if note != "" {
				res.Notes = append(res.Notes, note)
			}
		}
		if cfg.RequireVersionConstraints {
			res.Ran = appendUnique(res.Ran, "versions")
			found, err := VersionConstraints(dir)
			if err != nil {
				res.Notes = append(res.Notes, "could not parse "+display(env.Repo, dir)+": "+err.Error())
			}
			for i := range found {
				found[i].File = display(env.Repo, found[i].File)
			}
			versionIssues = append(versionIssues, scope(found, env.Repo, files, in.All)...)
		}
	}

	if len(initLines) > 0 {
		res.Checks = append(res.Checks, feedback.Check{
			Name: "init", Failed: true, Summary: cfg.Binary + " init -backend=false failed",
			Lines: initLines, Fix: "fix the configuration so the module initializes",
		})
	}
	if len(validateIssues) > 0 {
		res.Checks = append(res.Checks, feedback.Check{
			Name: "validate", Failed: true,
			Summary: fmt.Sprintf("%d error%s", len(validateIssues), plural(len(validateIssues))),
			Issues:  validateIssues, Fix: "fix the configuration errors above",
		})
	}
	if len(lintIssues) > 0 {
		res.Checks = append(res.Checks, lintCheck(lintIssues))
	}
	if len(scanIssues) > 0 {
		res.Checks = append(res.Checks, feedback.Check{
			Name: "trivy", Failed: true,
			Summary: fmt.Sprintf("%d HIGH/CRITICAL misconfiguration%s", len(scanIssues), plural(len(scanIssues))),
			Issues:  scanIssues, Fix: "apply each resolution; trivy ignore comments are not allowed",
		})
	}
	if len(versionIssues) > 0 {
		res.Checks = append(res.Checks, feedback.Check{
			Name: "versions", Failed: true,
			Summary: fmt.Sprintf("%d provider%s without a version constraint", len(versionIssues), plural(len(versionIssues))),
			Issues:  versionIssues,
			Fix:     "declare each provider in terraform { required_providers { ... } } with source and version",
		})
	}
	return res
}

// runTF runs the Terraform binary in dir with data and plugin cache under
// the plugin data directory.
func (m *Module) runTF(ctx context.Context, env module.Env, dir string, args ...string) (string, int, error) {
	bin := env.Config.Terraform.Binary
	cmd, err := env.Tool(ctx, bin)
	if err != nil {
		return "", 0, err
	}
	base := filepath.Join(env.DataDir, "terraform")
	cache := filepath.Join(base, "plugin-cache")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		return "", 0, err
	}
	full := []string{
		"TF_DATA_DIR=" + filepath.Join(base, state.Key(dir)),
		"TF_PLUGIN_CACHE_DIR=" + cache,
		"TF_IN_AUTOMATION=1",
		"TF_INPUT=0",
		cmd.Path,
	}
	full = append(append(full, cmd.Args...), args...)
	res, err := env.Runner.Run(ctx, dir, "env", full...)
	if err != nil {
		return "", 0, err
	}
	return res.Stdout + res.Stderr, res.ExitCode, nil
}

// networkHints mark init failures that are about reaching registries, not
// about the configuration.
var networkHints = []string{
	"Failed to query available provider packages", "Failed to install provider",
	"could not connect", "dial tcp", "no such host", "i/o timeout", "TLS handshake",
	"Failed to download module", "registry.terraform.io", "registry.opentofu.org",
	"connection refused", "network is unreachable",
}

// validate runs init and validate in dir. It returns validation issues,
// init failure lines that block, and a note for failures that do not.
func (m *Module) validate(ctx context.Context, env module.Env, dir string) ([]feedback.Issue, []string, string) {
	lock := filepath.Join(dir, ".terraform.lock.hcl")
	_, statErr := os.Stat(lock)
	hadLock := statErr == nil
	args := []string{"init", "-backend=false", "-input=false", "-no-color"}
	if hadLock {
		// The lockfile guard forbids editing the lock; never rewrite it.
		args = append(args, "-lockfile=readonly")
	}
	out, code, err := m.runTF(ctx, env, dir, args...)
	if !hadLock {
		// init writes a lock next to the code; the gate must leave no trace.
		_ = os.Remove(lock)
	}
	rel := display(env.Repo, dir)
	if err != nil {
		return nil, nil, "init could not run in " + rel + ": " + err.Error()
	}
	if code != 0 {
		for _, h := range networkHints {
			if strings.Contains(out, h) {
				return nil, nil, "init could not reach a registry for " + rel + "; validate skipped"
			}
		}
		return nil, prefixed(rel, errorLines(out, 8)), ""
	}

	out, _, err = m.runTF(ctx, env, dir, "validate", "-json", "-no-color")
	if err != nil {
		return nil, nil, "validate could not run in " + rel + ": " + err.Error()
	}
	issues, perr := parseValidate(out, env.Repo, dir)
	if perr != nil {
		return nil, nil, "could not parse validate output for " + rel
	}
	return issues, nil, ""
}

type validateReport struct {
	Diagnostics []struct {
		Severity string `json:"severity"`
		Summary  string `json:"summary"`
		Detail   string `json:"detail"`
		Range    *struct {
			Filename string `json:"filename"`
			Start    struct {
				Line   int `json:"line"`
				Column int `json:"column"`
			} `json:"start"`
		} `json:"range"`
	} `json:"diagnostics"`
}

func parseValidate(out, repo, dir string) ([]feedback.Issue, error) {
	start := strings.Index(out, "{")
	if start < 0 {
		return nil, errors.New("no JSON in validate output")
	}
	var rep validateReport
	if err := json.NewDecoder(strings.NewReader(out[start:])).Decode(&rep); err != nil {
		return nil, err
	}
	var issues []feedback.Issue
	for _, d := range rep.Diagnostics {
		if d.Severity != "error" {
			continue
		}
		msg := d.Summary
		if d.Detail != "" {
			msg += ": " + firstSentence(d.Detail)
		}
		is := feedback.Issue{File: display(repo, dir), Rule: "validate", Message: msg}
		if d.Range != nil {
			is.File = display(repo, filepath.Join(dir, d.Range.Filename))
			is.Line, is.Col = d.Range.Start.Line, d.Range.Start.Column
		}
		issues = append(issues, is)
	}
	return issues, nil
}

type trivyReport struct {
	Results []struct {
		Target            string `json:"Target"`
		Misconfigurations []struct {
			ID            string `json:"ID"`
			AVDID         string `json:"AVDID"`
			Title         string `json:"Title"`
			Message       string `json:"Message"`
			Severity      string `json:"Severity"`
			Status        string `json:"Status"`
			CauseMetadata struct {
				StartLine int `json:"StartLine"`
			} `json:"CauseMetadata"`
		} `json:"Misconfigurations"`
	} `json:"Results"`
}

func (m *Module) trivy(ctx context.Context, env module.Env, dir string) ([]feedback.Issue, string) {
	res, err := env.RunTool(ctx, dir, "trivy", "config", "--format", "json", "--quiet",
		"--severity", "HIGH,CRITICAL", "--cache-dir", filepath.Join(env.DataDir, "trivy"), ".")
	rel := display(env.Repo, dir)
	if err != nil {
		return nil, "trivy could not run in " + rel + ": " + err.Error()
	}
	if res.ExitCode != 0 {
		return nil, "trivy failed in " + rel + ": " + strings.Join(errorLines(res.Combined(), 1), "")
	}
	var rep trivyReport
	if err := json.Unmarshal([]byte(res.Stdout), &rep); err != nil {
		return nil, "could not parse trivy output for " + rel
	}
	var issues []feedback.Issue
	for _, r := range rep.Results {
		for _, mc := range r.Misconfigurations {
			if mc.Status != "" && mc.Status != "FAIL" {
				continue
			}
			id := mc.AVDID
			if id == "" {
				id = mc.ID
			}
			issues = append(issues, feedback.Issue{
				File: display(env.Repo, filepath.Join(dir, r.Target)),
				Line: mc.CauseMetadata.StartLine, Rule: id,
				Message: fmt.Sprintf("%s (%s): %s", mc.Title, mc.Severity, mc.Message),
			})
		}
	}
	return issues, ""
}

// moduleDirs returns the sorted, distinct directories of files that still
// exist.
func moduleDirs(files []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range files {
		dir := filepath.Dir(f)
		if seen[dir] {
			continue
		}
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		seen[dir] = true
		out = append(out, dir)
	}
	sort.Strings(out)
	return out
}

// only keeps issues in the given absolute files.
func only(issues []feedback.Issue, repo string, files []string) []feedback.Issue {
	want := map[string]bool{}
	for _, f := range files {
		want[display(repo, f)] = true
	}
	var out []feedback.Issue
	for _, is := range issues {
		if want[is.File] {
			out = append(out, is)
		}
	}
	return out
}

// scope keeps issues in touched files, or all of them in --all mode.
func scope(issues []feedback.Issue, repo string, files []string, all bool) []feedback.Issue {
	if all {
		return issues
	}
	return only(issues, repo, files)
}

func display(repo, p string) string {
	if rel, err := filepath.Rel(repo, p); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(p)
}

func allTerraformFiles(repo string) []string {
	var out []string
	m := New()
	_ = filepath.WalkDir(repo, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // unreadable paths are skipped, not fatal
		}
		if d.IsDir() && p != repo && (strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if !d.IsDir() && m.Match(p) {
			out = append(out, p)
		}
		return nil
	})
	return out
}

// errorLines returns up to n lines that carry an error, falling back to the
// first non-empty lines.
func errorLines(out string, n int) []string {
	var errs, all []string
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		all = append(all, l)
		if strings.HasPrefix(l, "Error") || strings.HasPrefix(l, "│ Error") {
			errs = append(errs, strings.TrimPrefix(l, "│ "))
		}
	}
	if len(errs) == 0 {
		errs = all
	}
	if len(errs) > n {
		errs = errs[:n]
	}
	return errs
}

func prefixed(prefix string, lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = prefix + ": " + l
	}
	return out
}

func firstSentence(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if i := strings.Index(s, ". "); i > 0 {
		return s[:i+1]
	}
	return s
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
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

var _ = tools.ErrMissing
