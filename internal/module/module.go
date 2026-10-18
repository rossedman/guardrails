// Package module defines the interface every language module implements.
//
// Adding a language is one package: implement Module and register it in
// the hook dispatcher.
package module

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"

	"github.com/rossedman/guardrails/internal/config"
	"github.com/rossedman/guardrails/internal/feedback"
	"github.com/rossedman/guardrails/internal/runner"
	"github.com/rossedman/guardrails/internal/tools"
)

// Env is what a module needs to do its work.
type Env struct {
	Runner runner.Runner
	Config config.Config
	// Repo is the repository root (the event's cwd, resolved to the git
	// top level when possible).
	Repo string
	// DataDir is the plugin data directory, for bundled configs and
	// scratch files.
	DataDir string
	// Tools resolves external tools, installing pinned versions when
	// allowed. Nil resolves from PATH through Runner.
	Tools tools.Resolver
}

// Tool resolves an external tool. Without a configured resolver the bare
// name is returned when Runner finds it on PATH.
func (e Env) Tool(ctx context.Context, name string) (tools.Command, error) {
	if e.Tools != nil {
		return e.Tools.Resolve(ctx, name)
	}
	if _, err := e.Runner.LookPath(name); err != nil {
		return tools.Command{}, fmt.Errorf("%s: %w", name, tools.ErrMissing)
	}
	return tools.Command{Path: name}, nil
}

// RunTool resolves name and runs it with args in dir.
func (e Env) RunTool(ctx context.Context, dir, name string, args ...string) (runner.Result, error) {
	cmd, err := e.Tool(ctx, name)
	if err != nil {
		return runner.Result{}, err
	}
	return e.Runner.Run(ctx, dir, cmd.Path, append(append([]string(nil), cmd.Args...), args...)...)
}

// Unavailable explains a tool that could not be resolved, for a note to
// the human or a failing check's summary.
func Unavailable(t Tool, err error) string {
	switch {
	case errors.Is(err, tools.ErrInstalling):
		return t.Name + " is being installed in the background; its checks are skipped until it is ready"
	case errors.Is(err, tools.ErrMissing):
		msg := t.Name + " is not installed"
		if t.Install != "" {
			msg += " (" + t.Install + ")"
		}
		return msg
	default:
		return t.Name + ": " + err.Error()
	}
}

// IsUnavailable reports whether err means a tool could not be resolved,
// as opposed to the tool running and failing.
func IsUnavailable(err error) bool {
	return errors.Is(err, tools.ErrInstalling) || errors.Is(err, tools.ErrMissing)
}

// Tool describes an external tool a module depends on.
type Tool struct {
	Name string
	// Required tools fail the gate when missing; optional ones only
	// produce a note for the human.
	Required bool
	// Install is a one-line install hint.
	Install string
	// VersionArgs prints the tool version, for `guardrails doctor`.
	VersionArgs []string
}

// FixResult reports what auto-fixing did.
type FixResult struct {
	// Notes are for the human (e.g. a formatter is missing).
	Notes []string
}

// GateInput scopes a gate run.
type GateInput struct {
	// Files are the absolute paths touched this session that the module
	// matches.
	Files []string
	// BaseCommit is HEAD at SessionStart, for patch coverage. Empty skips
	// patch coverage.
	BaseCommit string
	// Baseline maps package import path to coverage at SessionStart.
	Baseline map[string]float64
	// All runs over every package instead of only touched ones.
	All bool
}

// GateResult is the outcome of a gate run.
type GateResult struct {
	Checks []feedback.Check
	// Coverage maps package import path to measured coverage, for the
	// repo cache and ratchet.
	Coverage map[string]float64
	// Total is total coverage across measured packages, or -1 if not
	// measured.
	Total float64
	// Notes are for the human.
	Notes []string
	// Ran names every check that executed, passing or not.
	Ran []string
}

// Module is one language.
type Module interface {
	// Name is the module's short name, e.g. "go".
	Name() string
	// Enabled reports whether config turns the module on.
	Enabled(cfg config.Config) bool
	// Match reports whether the module owns path.
	Match(path string) bool
	// Tools lists external dependencies.
	Tools() []Tool
	// Standards is one paragraph telling Claude, at SessionStart, what the
	// module enforces under cfg.
	Standards(cfg config.Config) string
	// Fix auto-fixes files in place.
	Fix(ctx context.Context, env Env, files []string) FixResult
	// Check runs fast checks (<2s) on files and returns failing checks.
	Check(ctx context.Context, env Env, files []string) []feedback.Check
	// Gate runs the full quality gate.
	Gate(ctx context.Context, env Env, in GateInput) GateResult
}

var locRe = regexp.MustCompile(`^\s*(?:vet: )?((?:[A-Za-z]:)?[^\s:]+\.[A-Za-z0-9]+):(\d+)(?::(\d+))?: (.+)$`)

// ParseLocations extracts file:line[:col]: message issues from compiler-
// style output, tagging each with rule.
func ParseLocations(out, rule string) []feedback.Issue {
	var issues []feedback.Issue
	for _, l := range splitLines(out) {
		m := locRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		line, _ := strconv.Atoi(m[2])
		col, _ := strconv.Atoi(m[3])
		issues = append(issues, feedback.Issue{File: m[1], Line: line, Col: col, Rule: rule, Message: m[4]})
	}
	return issues
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
