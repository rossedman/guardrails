// Package engine turns hook events into decisions. Each call is a pure
// function of the event, the config, the session state and the outputs of
// the external tools it runs, which makes every event golden-testable.
package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rossedman/guardrails/internal/config"
	"github.com/rossedman/guardrails/internal/feedback"
	"github.com/rossedman/guardrails/internal/hook"
	"github.com/rossedman/guardrails/internal/module"
	"github.com/rossedman/guardrails/internal/module/golang"
	"github.com/rossedman/guardrails/internal/runlog"
	"github.com/rossedman/guardrails/internal/runner"
	"github.com/rossedman/guardrails/internal/state"
	"github.com/rossedman/guardrails/internal/tools"
)

// PostToolUseBudget bounds the per-edit work; the hook timeout is 5s.
const PostToolUseBudget = 4 * time.Second

// Engine handles hook events.
type Engine struct {
	Store   *state.Store
	Runner  runner.Runner
	Modules []module.Module
	// GlobalConfig is the global config path; empty skips it.
	GlobalConfig string
	// Now is the clock, replaceable in tests.
	Now func() time.Time
	// Tools provisions external tools. Nil resolves tools from PATH only.
	Tools *tools.Manager
}

// New returns an Engine with the default modules.
func New(store *state.Store, r runner.Runner, globalConfig string) *Engine {
	return &Engine{
		Store:        store,
		Runner:       r,
		Modules:      []module.Module{golang.New()},
		GlobalConfig: globalConfig,
		Now:          time.Now,
	}
}

// Handle dispatches one event.
func (e *Engine) Handle(ctx context.Context, event string, in hook.Input) (hook.Output, error) {
	if in.SessionID == "" {
		return hook.Output{}, errors.New("hook input has no session_id")
	}
	switch event {
	case hook.SessionStart:
		return e.sessionStart(ctx, in)
	case hook.PreToolUse:
		return e.preToolUse(ctx, in)
	case hook.PostToolUse:
		return e.postToolUse(ctx, in)
	case hook.Stop:
		return e.stop(ctx, in, "")
	case hook.SubagentStop:
		return e.stop(ctx, in, agentKey(in))
	case hook.SessionEnd:
		return e.sessionEnd(in)
	default:
		return hook.Output{}, fmt.Errorf("unknown hook event %q", event)
	}
}

// NormalizeEvent maps CLI spellings (post-tool-use, posttooluse) to the
// canonical event name.
func NormalizeEvent(s string) (string, bool) {
	key := strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(s))
	for _, ev := range []string{hook.SessionStart, hook.PreToolUse, hook.PostToolUse, hook.Stop, hook.SubagentStop, hook.SessionEnd} {
		if strings.ToLower(ev) == key {
			return ev, true
		}
	}
	return "", false
}

// Blocking reports whether a failure of this event's hook should block.
func Blocking(event string) bool {
	switch event {
	case hook.PreToolUse, hook.PostToolUse, hook.Stop, hook.SubagentStop:
		return true
	}
	return false
}

// RepoRoot resolves the repository root for cwd: the git top level when
// cwd is inside a work tree, else cwd itself.
func (e *Engine) RepoRoot(ctx context.Context, cwd string) string {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	res, err := e.Runner.Run(ctx, cwd, "git", "rev-parse", "--show-toplevel")
	if err == nil && res.ExitCode == 0 {
		if top := strings.TrimSpace(res.Stdout); top != "" {
			return filepath.Clean(top)
		}
	}
	return filepath.Clean(cwd)
}

func (e *Engine) loadConfig(repo string) (config.Config, error) {
	return config.Load(e.GlobalConfig, repo)
}

func (e *Engine) env(repo string, cfg config.Config, blocking bool) module.Env {
	env := module.Env{Runner: e.Runner, Config: cfg, Repo: repo, DataDir: e.Store.Root}
	if m := e.manager(cfg, blocking); m != nil {
		env.Tools = m
	}
	return env
}

// manager returns the tool manager configured for cfg, or nil when the
// engine has none. Blocking managers install missing tools synchronously
// (the Stop gate and the CLI); non-blocking ones start a background install
// and skip the check (per-edit hooks, which have a 5s budget).
func (e *Engine) manager(cfg config.Config, blocking bool) *tools.Manager {
	if e.Tools == nil {
		return nil
	}
	m := e.Tools.WithBlocking(blocking)
	m.Prefer = cfg.Tools.Prefer
	m.AutoInstall = cfg.Tools.Install != config.InstallNever
	return m
}

// moduleFor returns the enabled module owning path, if any.
func (e *Engine) moduleFor(cfg config.Config, path string) module.Module {
	for _, m := range e.Modules {
		if m.Enabled(cfg) && m.Match(path) {
			return m
		}
	}
	return nil
}

func configError(err error) hook.Output {
	return hook.Output{SystemMessage: "guardrails: config error, checks are paused until it is fixed: " + err.Error()}
}

func agentKey(in hook.Input) string {
	if in.AgentID != "" {
		return in.AgentID
	}
	return in.AgentType
}

func absPath(cwd, p string) string {
	if p == "" {
		return ""
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(cwd, p)
}

func within(repo, p string) bool {
	rel, err := filepath.Rel(repo, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}

func notes(ns []string) string {
	if len(ns) == 0 {
		return ""
	}
	return "guardrails: " + strings.Join(ns, "; ")
}

func failures(checks []feedback.Check) []runlog.Failure {
	var out []runlog.Failure
	for _, c := range checks {
		if !c.Failed {
			continue
		}
		if len(c.Issues) == 0 {
			out = append(out, runlog.Failure{Check: c.Name})
			continue
		}
		for _, is := range c.Issues {
			out = append(out, runlog.Failure{Check: c.Name, Rule: is.Rule, File: is.File})
		}
	}
	return out
}

func countIssues(checks []feedback.Check) int {
	n := 0
	for _, c := range checks {
		if c.Name == "lint" || c.Name == "vet" {
			n += len(c.Issues)
		}
	}
	return n
}

func sortedKeys(m map[string]float64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
