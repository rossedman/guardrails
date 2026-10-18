package golang

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/rossedman/guardrails/internal/module"
)

// testDependents runs the tests of every package in the module that imports
// a touched package, directly or transitively, so a change that breaks its
// callers can't pass the gate. These run without -count=1: Go's test cache
// keys on every dependency's content, so packages the change can't affect
// are not re-run, and the ones it can are.
func (m *Module) testDependents(ctx context.Context, env module.Env, root, modPath string, pkgs []string, out *moduleResult) {
	deps, err := dependents(ctx, env, root, modPath, pkgs)
	if err != nil {
		out.notes = append(out.notes, "dependent packages were not tested: "+err.Error())
		return
	}
	if len(deps) == 0 {
		return
	}
	out.ran = append(out.ran, "dependents")
	args := []string{"test"}
	if env.Config.Go.Race {
		args = append(args, "-race")
	}
	res, err := env.Runner.Run(ctx, root, "go", append(args, deps...)...)
	if err != nil {
		out.checks = append(out.checks, toolError("dependents", err))
		return
	}
	if res.ExitCode == 0 {
		return
	}
	c := testFailure(env.Repo, root, modPath, res.Combined())
	c.Name = "dependents"
	c.Summary = fmt.Sprintf("%s in packages that import your changes", strings.TrimPrefix(c.Summary, "go test failed"))
	if strings.HasPrefix(c.Summary, " in packages") {
		c.Summary = "tests failed" + c.Summary
	}
	c.Fix = "your change broke code that depends on it: fix the callers or keep the old behavior; do not skip or delete their tests"
	out.checks = append(out.checks, c)
}

// dependents returns ./rel patterns for the module's packages that import
// any of pkgs (./rel patterns), transitively, excluding pkgs themselves.
// Test imports count: a package whose tests use a touched package depends
// on it too.
func dependents(ctx context.Context, env module.Env, root, modPath string, pkgs []string) ([]string, error) {
	res, err := env.Runner.Run(ctx, root, "go", "list", "-e",
		"-f", "{{.ImportPath}}|{{join .Imports \",\"}}|{{join .TestImports \",\"}}|{{join .XTestImports \",\"}}", "./...")
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("go list: %s", strings.Join(firstLines(res.Combined(), 3), " "))
	}
	importers := map[string][]string{}
	inModule := map[string]bool{}
	for _, line := range strings.Split(res.Stdout, "\n") {
		parts := strings.Split(strings.TrimSpace(line), "|")
		if len(parts) != 4 || parts[0] == "" {
			continue
		}
		pkg := parts[0]
		inModule[pkg] = true
		seen := map[string]bool{}
		for _, list := range parts[1:] {
			for _, imp := range strings.Split(list, ",") {
				if imp == "" || imp == pkg || seen[imp] {
					continue
				}
				seen[imp] = true
				importers[imp] = append(importers[imp], pkg)
			}
		}
	}

	touched := map[string]bool{}
	var queue []string
	for _, p := range pkgs {
		ip := importPath(modPath, p)
		touched[ip] = true
		queue = append(queue, ip)
	}
	found := map[string]bool{}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, imp := range importers[cur] {
			if touched[imp] || found[imp] || !inModule[imp] {
				continue
			}
			found[imp] = true
			queue = append(queue, imp)
		}
	}
	out := make([]string, 0, len(found))
	for ip := range found {
		out = append(out, relPattern(modPath, ip))
	}
	sort.Strings(out)
	return out, nil
}

// importPath converts a ./rel pattern to an import path.
func importPath(modPath, pattern string) string {
	rel := strings.TrimPrefix(strings.TrimPrefix(pattern, "./"), ".")
	if rel == "" {
		return modPath
	}
	return path.Join(modPath, rel)
}

// relPattern converts an import path in the module to a ./rel pattern.
func relPattern(modPath, ip string) string {
	if ip == modPath {
		return "."
	}
	return "./" + strings.TrimPrefix(ip, modPath+"/")
}
