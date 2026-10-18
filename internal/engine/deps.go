package engine

import (
	"context"
	"path/filepath"

	"github.com/rossedman/guardrails/internal/config"
	"github.com/rossedman/guardrails/internal/deps"
	"github.com/rossedman/guardrails/internal/guard"
)

// checkDeps looks up the packages a tool call adds: install commands for
// Bash, added dependencies for manifest edits.
func (e *Engine) checkDeps(ctx context.Context, cfg config.Config, reqs []deps.Request) guard.Decision {
	if !cfg.DepsEnabled() || len(reqs) == 0 {
		return guard.Allowed
	}
	c := e.Deps
	if c == nil {
		c = deps.NewChecker(filepath.Join(e.Store.Root, "deps-cache"))
	}
	var ds []guard.Decision
	for _, f := range c.Check(ctx, cfg.Deps, reqs) {
		ds = append(ds, guard.Decision{Action: f.Action, Guard: "deps", Reason: f.Reason})
	}
	return guard.Worst(ds...)
}
