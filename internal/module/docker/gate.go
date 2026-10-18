package docker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/rossedman/guardrails/internal/feedback"
	"github.com/rossedman/guardrails/internal/module"
)

// Gate implements module.Module: the native rules over every touched
// Dockerfile, then `docker build --check` when a daemon is reachable.
func (m *Module) Gate(ctx context.Context, env module.Env, in module.GateInput) module.GateResult {
	res := module.GateResult{Total: -1}
	files := in.Files
	if in.All {
		files = m.allDockerfiles(env.Repo)
	}
	var existing []string
	for _, f := range files {
		if info, err := os.Stat(f); err == nil && !info.IsDir() {
			existing = append(existing, f)
		}
	}
	if len(existing) == 0 {
		return res
	}
	res.Ran = append(res.Ran, "dockerfile-rules")
	res.Checks = append(res.Checks, lintFiles(env, existing)...)
	if env.Config.Docker.BuildCheck {
		m.buildCheck(ctx, env, existing, &res)
	}
	return res
}

func (m *Module) buildCheck(ctx context.Context, env module.Env, files []string, res *module.GateResult) {
	if _, err := env.Tool(ctx, "docker"); err != nil {
		res.Notes = append(res.Notes, "docker build --check skipped: "+module.Unavailable(m.Tools()[0], err))
		return
	}
	info, err := env.RunTool(ctx, env.Repo, "docker", "info", "--format", "{{.ServerVersion}}")
	if err != nil || info.ExitCode != 0 {
		res.Notes = append(res.Notes, "docker build --check skipped: no Docker daemon")
		return
	}
	res.Ran = append(res.Ran, "build-check")
	var issues []feedback.Issue
	var failures []string
	for _, f := range files {
		name := rel(env.Repo, f)
		out, err := env.RunTool(ctx, env.Repo, "docker", "build", "--check", "-f", f, filepath.Dir(f))
		if err != nil {
			failures = append(failures, name+": "+err.Error())
			continue
		}
		found := parseBuildCheck(name, out.Combined())
		issues = append(issues, found...)
		if out.ExitCode != 0 && len(found) == 0 {
			failures = append(failures, firstLines(out.Combined(), 3)...)
		}
	}
	if len(issues) == 0 && len(failures) == 0 {
		return
	}
	n := max(len(issues), 1)
	res.Checks = append(res.Checks, feedback.Check{
		Name: "build-check", Failed: true,
		Summary: fmt.Sprintf("docker build --check reported %d issue%s", n, plural(n)),
		Issues:  issues,
		Lines:   failures,
		Fix:     "follow each rule's guidance (https://docs.docker.com/reference/build-checks/)",
	})
}

var (
	warnRe     = regexp.MustCompile(`^\s*WARNING:\s*([A-Za-z0-9]+)\s*(?:-\s*(\S+))?`)
	lineHintRe = regexp.MustCompile(`\(line (\d+)\)`)
	fileLineRe = regexp.MustCompile(`^\s*\S+:(\d+)\s*$`)
)

// parseBuildCheck reads `docker build --check` output:
//
//	WARNING: FromAsCasing - https://docs.docker.com/go/dockerfile/rule/from-as-casing/
//	'as' and 'FROM' keywords' casing do not match (line 1)
func parseBuildCheck(file, out string) []feedback.Issue {
	var issues []feedback.Issue
	lines := strings.Split(out, "\n")
	for i := 0; i < len(lines); i++ {
		m := warnRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		is := feedback.Issue{File: file, Rule: m[1], Message: m[1]}
		for j := i + 1; j < len(lines) && j <= i+3; j++ {
			t := strings.TrimSpace(lines[j])
			if t == "" || warnRe.MatchString(t) {
				break
			}
			if lm := lineHintRe.FindStringSubmatch(t); lm != nil {
				is.Line, _ = strconv.Atoi(lm[1])
				is.Message = strings.TrimSpace(lineHintRe.ReplaceAllString(t, ""))
				break
			}
			if fm := fileLineRe.FindStringSubmatch(t); fm != nil && is.Line == 0 {
				is.Line, _ = strconv.Atoi(fm[1])
				continue
			}
			if is.Message == is.Rule {
				is.Message = t
			}
		}
		issues = append(issues, is)
	}
	return issues
}

// allDockerfiles finds every Dockerfile under repo, skipping hidden,
// vendor and node_modules directories.
func (m *Module) allDockerfiles(repo string) []string {
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
			if p != repo && (strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if m.Match(p) {
			out = append(out, p)
		}
		return nil
	})
	return out
}
