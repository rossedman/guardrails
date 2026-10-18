// Package testdepth checks that tests do more than execute code: mutation
// testing of changed functions, benchmarks for hot paths (with regression
// checks against the session's base commit), and fuzz targets for code that
// parses untrusted input.
//
// Everything is scoped to functions changed this session, and every check
// runs inside its own time slice so the Stop gate stays within budget.
package testdepth

import (
	"context"
	"time"

	"github.com/rossedman/guardrails/internal/coverage"
	"github.com/rossedman/guardrails/internal/feedback"
	"github.com/rossedman/guardrails/internal/module"
)

// Env is what the checks need beyond module.Env.
type Env struct {
	module.Env
	// Root is the Go module root; ModPath its import path.
	Root    string
	ModPath string
	// Base is the commit the session started from; empty disables checks
	// that compare against it.
	Base string
	// Changed maps absolute file paths to the lines changed this session.
	Changed map[string]coverage.LineSet
	// Asked holds keys of borderline requirements already raised this
	// session; they are not raised again.
	Asked map[string]bool
	// Now is the clock.
	Now func() time.Time
}

// Result is what one check contributes to the gate.
type Result struct {
	Checks []feedback.Check
	Notes  []string
	Ran    []string
	// Asked are keys of borderline requirements raised now, to record so
	// they are asked only once.
	Asked []string
}

// Merge appends r2 to r.
func (r *Result) Merge(r2 Result) {
	r.Checks = append(r.Checks, r2.Checks...)
	r.Notes = append(r.Notes, r2.Notes...)
	r.Ran = append(r.Ran, r2.Ran...)
	r.Asked = append(r.Asked, r2.Asked...)
}

// Slice returns a context bounded by budget and by ctx's own deadline,
// whichever comes first.
func Slice(ctx context.Context, budget time.Duration) (context.Context, context.CancelFunc) {
	if budget <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, budget)
}
