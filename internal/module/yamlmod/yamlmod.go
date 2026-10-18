// Package yamlmod is the YAML language module: yamllint on every YAML
// file, actionlint on GitHub Actions workflows, and kubeconform on
// Kubernetes manifests. YAML has no auto-fixer; the same checks run on edit
// and, over every touched file, at the Stop gate.
package yamlmod

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/rossedman/guardrails/internal/config"
	"github.com/rossedman/guardrails/internal/feedback"
	"github.com/rossedman/guardrails/internal/module"
)

// Module implements module.Module for YAML.
type Module struct{}

// New returns the YAML module.
func New() *Module { return &Module{} }

// Name implements module.Module.
func (*Module) Name() string { return "yaml" }

// Enabled implements module.Module.
func (*Module) Enabled(cfg config.Config) bool { return cfg.YAMLEnabled() }

// Match implements module.Module.
func (*Module) Match(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".yml" || ext == ".yaml"
}

// Tools implements module.Module.
func (*Module) Tools() []module.Tool {
	return []module.Tool{
		{Name: "yamllint", Required: true, Install: "managed, or pipx install yamllint", VersionArgs: []string{"--version"}},
		{Name: "actionlint", Required: true, Install: "managed, or go install github.com/rhysd/actionlint/cmd/actionlint@latest", VersionArgs: []string{"-version"}},
		{Name: "kubeconform", Required: true, Install: "managed, or go install github.com/yannh/kubeconform/cmd/kubeconform@latest", VersionArgs: []string{"-v"}},
	}
}

// Standards implements module.Module.
func (*Module) Standards(cfg config.Config) string {
	var b strings.Builder
	b.WriteString("YAML standards: every YAML file you edit must pass yamllint (errors block; the repo's .yamllint config applies, else the relaxed preset)")
	if cfg.YAML.Actionlint {
		b.WriteString("; GitHub Actions workflows must pass actionlint")
	}
	if cfg.YAML.Kubeconform.Enabled {
		fmt.Fprintf(&b, "; Kubernetes manifests must validate against the Kubernetes %s schemas with kubeconform in strict mode", cfg.YAML.Kubeconform.KubernetesVersion)
	}
	b.WriteString(".")
	return b.String()
}

// Fix implements module.Module. YAML has no safe auto-fixer.
func (*Module) Fix(context.Context, module.Env, []string) module.FixResult {
	return module.FixResult{}
}

// Check implements module.Module: the same checks as the gate, for the
// edited files. Unavailable tools are skipped; the gate reports them.
func (m *Module) Check(ctx context.Context, env module.Env, files []string) []feedback.Check {
	r := m.run(ctx, env, existing(files), false)
	return r.Checks
}

// Gate implements module.Module.
func (m *Module) Gate(ctx context.Context, env module.Env, in module.GateInput) module.GateResult {
	files := in.Files
	if in.All {
		files = allYAML(env.Repo)
	}
	return m.run(ctx, env, existing(files), true)
}

// run checks files. At the gate, unavailable required tools fail closed.
func (m *Module) run(ctx context.Context, env module.Env, files []string, gate bool) module.GateResult {
	res := module.GateResult{Total: -1}
	if len(files) == 0 {
		return res
	}
	cfg := env.Config.YAML
	var workflows, manifests []string
	for _, f := range files {
		if cfg.Actionlint && isWorkflow(env.Repo, f) {
			workflows = append(workflows, f)
		}
		if cfg.Kubeconform.Enabled && isManifest(f) {
			manifests = append(manifests, f)
		}
	}

	var unavailable []string
	tool := func(name string) bool {
		_, err := env.Tool(ctx, name)
		if err == nil {
			return true
		}
		if gate {
			unavailable = append(unavailable, module.Unavailable(m.tool(name), err))
		}
		return false
	}

	if tool("yamllint") {
		res.Ran = append(res.Ran, "yamllint")
		if c, failed := m.yamllint(ctx, env, files); failed {
			res.Checks = append(res.Checks, c)
		}
	}
	if len(workflows) > 0 && tool("actionlint") {
		res.Ran = append(res.Ran, "actionlint")
		if c, failed := m.actionlint(ctx, env, workflows); failed {
			res.Checks = append(res.Checks, c)
		}
	}
	if len(manifests) > 0 && tool("kubeconform") {
		res.Ran = append(res.Ran, "kubeconform")
		c, failed, notes := m.kubeconform(ctx, env, manifests)
		if failed {
			res.Checks = append(res.Checks, c)
		}
		res.Notes = append(res.Notes, notes...)
	}
	if len(unavailable) > 0 {
		res.Checks = append(res.Checks, feedback.Check{
			Name: "tools", Failed: true, Summary: strings.Join(unavailable, "; "),
			Fix: "the human must install the missing tools or set tools.install: auto",
		})
	}
	return res
}

func (m *Module) tool(name string) module.Tool {
	for _, t := range m.Tools() {
		if t.Name == name {
			return t
		}
	}
	return module.Tool{Name: name}
}

var parsableRe = regexp.MustCompile(`^(.+?):(\d+):(\d+): \[(error|warning)\] (.*?)(?: \(([\w-]+)\))?$`)

func (m *Module) yamllint(ctx context.Context, env module.Env, files []string) (feedback.Check, bool) {
	args := []string{"-f", "parsable"}
	if !hasYamllintConfig(env.Repo) {
		args = append(args, "-d", "relaxed")
	}
	out, err := env.RunTool(ctx, env.Repo, "yamllint", append(args, rel(env.Repo, files)...)...)
	if err != nil {
		return toolError("yamllint", err), true
	}
	var issues []feedback.Issue
	for _, l := range strings.Split(out.Stdout, "\n") {
		mm := parsableRe.FindStringSubmatch(strings.TrimSpace(l))
		if mm == nil || mm[4] != "error" {
			continue
		}
		line, _ := strconv.Atoi(mm[2])
		col, _ := strconv.Atoi(mm[3])
		issues = append(issues, feedback.Issue{
			File: display(env.Repo, mm[1]), Line: line, Col: col, Rule: orDefault(mm[6], "yamllint"), Message: mm[5],
		})
	}
	if len(issues) == 0 {
		if out.ExitCode != 0 && strings.TrimSpace(out.Stdout) == "" {
			return feedback.Check{
				Name: "yamllint", Failed: true, Summary: "yamllint failed to run",
				Lines: firstLines(out.Combined(), 10),
			}, true
		}
		return feedback.Check{}, false
	}
	return feedback.Check{
		Name: "yamllint", Failed: true, Summary: countSummary(len(issues), "error"),
		Issues: issues, Fix: "fix the YAML errors; duplicate keys and syntax errors change meaning",
	}, true
}

type actionlintError struct {
	Message  string `json:"message"`
	Filepath string `json:"filepath"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Kind     string `json:"kind"`
}

func (m *Module) actionlint(ctx context.Context, env module.Env, files []string) (feedback.Check, bool) {
	out, err := env.RunTool(ctx, env.Repo, "actionlint", append([]string{"-format", "{{json .}}"}, rel(env.Repo, files)...)...)
	if err != nil {
		return toolError("actionlint", err), true
	}
	var errs []actionlintError
	body := strings.TrimSpace(out.Stdout)
	if body != "" && body != "null" {
		if err := json.Unmarshal([]byte(body), &errs); err != nil {
			return feedback.Check{
				Name: "actionlint", Failed: true, Summary: "could not parse actionlint output",
				Lines: firstLines(out.Combined(), 10),
			}, true
		}
	}
	if len(errs) == 0 {
		if out.ExitCode > 1 {
			return feedback.Check{
				Name: "actionlint", Failed: true, Summary: "actionlint failed to run",
				Lines: firstLines(out.Combined(), 10),
			}, true
		}
		return feedback.Check{}, false
	}
	issues := make([]feedback.Issue, 0, len(errs))
	for _, e := range errs {
		issues = append(issues, feedback.Issue{
			File: display(env.Repo, e.Filepath), Line: e.Line, Col: e.Column,
			Rule: orDefault(e.Kind, "actionlint"), Message: e.Message,
		})
	}
	return feedback.Check{
		Name: "actionlint", Failed: true, Summary: countSummary(len(issues), "issue"),
		Issues: issues, Fix: "fix the workflow; actionlint catches errors GitHub would only report at run time",
	}, true
}

type kubeconformReport struct {
	Resources []struct {
		Filename         string `json:"filename"`
		Kind             string `json:"kind"`
		Name             string `json:"name"`
		Status           string `json:"status"`
		Msg              string `json:"msg"`
		ValidationErrors []struct {
			Path string `json:"path"`
			Msg  string `json:"msg"`
		} `json:"validationErrors"`
	} `json:"resources"`
}

func (m *Module) kubeconform(ctx context.Context, env module.Env, files []string) (feedback.Check, bool, []string) {
	args := []string{
		"-strict", "-summary=false", "-output", "json", "-ignore-missing-schemas",
		"-kubernetes-version", k8sVersion(env.Config.YAML.Kubeconform.KubernetesVersion),
	}
	if env.DataDir != "" {
		cache := filepath.Join(env.DataDir, "kubeconform-cache")
		if err := os.MkdirAll(cache, 0o700); err == nil {
			args = append(args, "-cache", cache)
		}
	}
	out, err := env.RunTool(ctx, env.Repo, "kubeconform", append(args, rel(env.Repo, files)...)...)
	if err != nil {
		return toolError("kubeconform", err), true, nil
	}
	var rep kubeconformReport
	if err := json.Unmarshal([]byte(out.Stdout), &rep); err != nil {
		return feedback.Check{
			Name: "kubeconform", Failed: true, Summary: "could not parse kubeconform output",
			Lines: firstLines(out.Combined(), 10),
		}, true, nil
	}
	var (
		issues  []feedback.Issue
		offline int
	)
	for _, r := range rep.Resources {
		resource := strings.TrimSpace(r.Kind + "/" + r.Name)
		switch {
		case r.Status == "statusError" && strings.Contains(r.Msg, "failed downloading schema"):
			offline++
		case r.Status == "statusInvalid" && len(r.ValidationErrors) > 0:
			for _, v := range r.ValidationErrors {
				issues = append(issues, feedback.Issue{
					File: display(env.Repo, r.Filename), Rule: resource,
					Message: strings.TrimSpace(orDefault(v.Path, "/") + ": " + v.Msg),
				})
			}
		case r.Status == "statusInvalid" || r.Status == "statusError":
			issues = append(issues, feedback.Issue{File: display(env.Repo, r.Filename), Rule: resource, Message: r.Msg})
		}
	}
	var notes []string
	if offline > 0 {
		notes = append(notes, fmt.Sprintf("kubeconform could not download schemas for %d resource(s); validation skipped (offline?)", offline))
	}
	if len(issues) == 0 {
		return feedback.Check{}, false, notes
	}
	return feedback.Check{
		Name: "kubeconform", Failed: true, Summary: countSummary(len(issues), "schema violation"),
		Issues: issues, Fix: "fix the manifests to match the Kubernetes API schema",
	}, true, notes
}

// k8sVersion expands "1.31" to the "1.31.0" kubeconform requires.
func k8sVersion(v string) string {
	if v == "" || v == "master" {
		return "master"
	}
	if strings.Count(v, ".") == 1 {
		return v + ".0"
	}
	return v
}

// isWorkflow reports whether f is a GitHub Actions workflow.
func isWorkflow(repo, f string) bool {
	r := display(repo, f)
	return strings.HasPrefix(r, ".github/workflows/") && !strings.Contains(strings.TrimPrefix(r, ".github/workflows/"), "/")
}

// isManifest reports whether any document in f has top-level apiVersion
// and kind. Helm templates are skipped: they are not YAML until rendered.
func isManifest(f string) bool {
	data, err := os.ReadFile(f)
	if err != nil {
		return false
	}
	if strings.Contains(filepath.ToSlash(f), "/templates/") && bytes.Contains(data, []byte("{{")) {
		return false
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var node yaml.Node
		if err := dec.Decode(&node); err != nil {
			// EOF, or invalid YAML: the decoder cannot resume after a
			// syntax error, and yamllint reports it anyway.
			return false
		}
		var doc map[string]any
		if node.Decode(&doc) != nil {
			continue // a list or scalar document
		}
		_, hasAPI := doc["apiVersion"]
		_, hasKind := doc["kind"]
		if hasAPI && hasKind {
			return true
		}
	}
}

func hasYamllintConfig(repo string) bool {
	for _, n := range []string{".yamllint", ".yamllint.yaml", ".yamllint.yml"} {
		if _, err := os.Stat(filepath.Join(repo, n)); err == nil {
			return true
		}
	}
	return false
}

// existing keeps files that still exist.
func existing(files []string) []string {
	var out []string
	for _, f := range files {
		if info, err := os.Stat(f); err == nil && !info.IsDir() {
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}

// allYAML lists YAML files under repo, skipping hidden directories other
// than .github, vendor and node_modules.
func allYAML(repo string) []string {
	var out []string
	_ = filepath.WalkDir(repo, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return skipUnreadable(d)
		}
		if d.IsDir() {
			name := d.Name()
			if p != repo && ((strings.HasPrefix(name, ".") && name != ".github") || name == "vendor" || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if New().Match(p) {
			out = append(out, p)
		}
		return nil
	})
	return out
}

// skipUnreadable skips an unreadable directory and ignores an unreadable
// file, so one bad entry doesn't abort the walk.
func skipUnreadable(d os.DirEntry) error {
	if d != nil && d.IsDir() {
		return filepath.SkipDir
	}
	return nil
}

func rel(repo string, files []string) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = display(repo, f)
	}
	return out
}

func display(repo, p string) string {
	if filepath.IsAbs(p) {
		if r, err := filepath.Rel(repo, p); err == nil && !strings.HasPrefix(r, "..") {
			return filepath.ToSlash(r)
		}
	}
	return filepath.ToSlash(p)
}

func toolError(name string, err error) feedback.Check {
	return feedback.Check{Name: name, Failed: true, Summary: "could not run: " + err.Error()}
}

func countSummary(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
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
