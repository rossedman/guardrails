package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/rossedman/guardrails/internal/config"
	"github.com/rossedman/guardrails/internal/feedback"
	"github.com/rossedman/guardrails/internal/module"
)

// GateOutcome is the merged result of every module's gate.
type GateOutcome struct {
	Checks   []feedback.Check
	Notes    []string
	Coverage map[string]float64
	// Total is the last measured total coverage, or -1.
	Total float64
	// Modules names the modules that ran.
	Modules []string
	// Ran counts the checks that executed.
	Ran int
}

// Gate runs every enabled module's gate over the files it owns. all runs
// over every package instead of only the files' packages.
func (e *Engine) Gate(ctx context.Context, repo string, cfg config.Config, files []string, base string, baseline map[string]float64, all bool) GateOutcome {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(cfg.Gate.TimeoutSeconds)*time.Second)
	defer cancel()
	env := e.env(repo, cfg)

	out := GateOutcome{Coverage: map[string]float64{}, Total: -1}
	for _, m := range e.Modules {
		if !m.Enabled(cfg) {
			continue
		}
		var mine []string
		for _, f := range files {
			if m.Match(f) {
				mine = append(mine, f)
			}
		}
		if len(mine) == 0 && !all {
			continue
		}
		out.Modules = append(out.Modules, m.Name())
		res := m.Gate(ctx, env, module.GateInput{Files: mine, BaseCommit: base, Baseline: baseline, All: all})
		out.Checks = append(out.Checks, res.Checks...)
		out.Notes = append(out.Notes, res.Notes...)
		out.Ran += len(res.Ran)
		for k, v := range res.Coverage {
			out.Coverage[k] = v
		}
		if res.Total >= 0 {
			out.Total = res.Total
		}
	}
	if ctx.Err() != nil {
		out.Checks = append(out.Checks, feedback.Check{
			Name: "timeout", Failed: true,
			Summary: fmt.Sprintf("the gate exceeded its %ds budget", cfg.Gate.TimeoutSeconds),
			Fix:     "the human may need to raise gate.timeout_seconds or split slow tests",
		})
	}
	return out
}
