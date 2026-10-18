// Package docker is the Docker language module. Guardrails checks
// Dockerfiles with its own parser and native rules, so no linter needs to be
// installed: digest-pinned base images, a non-root final stage, and a few
// high-signal hygiene rules on every edit; the Stop gate repeats them and,
// when a Docker daemon is available, runs `docker build --check`.
package docker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rossedman/guardrails/internal/config"
	"github.com/rossedman/guardrails/internal/feedback"
	"github.com/rossedman/guardrails/internal/module"
)

// Module implements module.Module for Dockerfiles.
type Module struct{}

// New returns the Docker module.
func New() *Module { return &Module{} }

// Name implements module.Module.
func (*Module) Name() string { return "docker" }

// Enabled implements module.Module.
func (*Module) Enabled(cfg config.Config) bool { return cfg.DockerEnabled() }

// Match implements module.Module.
func (*Module) Match(path string) bool {
	base := filepath.Base(path)
	switch {
	case base == "Dockerfile", base == "Containerfile":
		return true
	case strings.HasPrefix(base, "Dockerfile."), strings.HasPrefix(base, "Containerfile."):
		return true
	}
	return strings.HasSuffix(strings.ToLower(base), ".dockerfile")
}

// Tools implements module.Module. The native rules need nothing; the docker
// CLI is only used for `docker build --check`.
func (*Module) Tools() []module.Tool {
	return []module.Tool{
		{Name: "docker", Install: "https://docs.docker.com/get-docker/ (only for docker build --check)", VersionArgs: []string{"--version"}},
	}
}

// Standards implements module.Module.
func (*Module) Standards(cfg config.Config) string {
	d := cfg.Docker
	var parts []string
	if d.RequireDigestPins {
		parts = append(parts, "pin every base image by digest (image:tag@sha256:...)")
	} else {
		parts = append(parts, "use explicit, non-latest image tags")
	}
	if d.RequireNonRoot {
		parts = append(parts, "set a non-root USER in the final stage")
	}
	parts = append(parts, "use COPY rather than ADD for local files",
		"install packages with --no-install-recommends / --no-cache and clean apt lists",
		"never put secrets in ARG or ENV")
	s := "Docker standards, checked on each edited Dockerfile: " + strings.Join(parts, ", ") + "."
	if d.BuildCheck {
		s += " The Stop gate also runs `docker build --check` when a Docker daemon is available."
	}
	return s
}

// Fix implements module.Module. Dockerfiles have no standard formatter.
func (*Module) Fix(context.Context, module.Env, []string) module.FixResult {
	return module.FixResult{}
}

// Check implements module.Module: the native rules on the edited files.
func (*Module) Check(_ context.Context, env module.Env, files []string) []feedback.Check {
	return lintFiles(env, files)
}

// lintFiles runs the native rules over files and merges the checks by rule.
func lintFiles(env module.Env, files []string) []feedback.Check {
	merged := map[string]*feedback.Check{}
	var order []string
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, c := range Lint(env.Config.Docker, rel(env.Repo, f), string(data)) {
			if prev, ok := merged[c.Name]; ok {
				prev.Issues = append(prev.Issues, c.Issues...)
				continue
			}
			merged[c.Name] = &c
			order = append(order, c.Name)
		}
	}
	checks := make([]feedback.Check, 0, len(order))
	for _, name := range order {
		c := merged[name]
		c.Summary = fmt.Sprintf("%d issue%s", len(c.Issues), plural(len(c.Issues)))
		checks = append(checks, *c)
	}
	if len(checks) == 0 {
		return nil
	}
	return checks
}

// rel renders p relative to repo with forward slashes when it is inside it.
func rel(repo, p string) string {
	if !filepath.IsAbs(p) {
		return filepath.ToSlash(p)
	}
	if r, err := filepath.Rel(repo, p); err == nil && !strings.HasPrefix(r, "..") {
		return filepath.ToSlash(r)
	}
	return filepath.ToSlash(p)
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
