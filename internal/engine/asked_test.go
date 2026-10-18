package engine

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/rossedman/guardrails/internal/config"
	"github.com/rossedman/guardrails/internal/feedback"
	"github.com/rossedman/guardrails/internal/hook"
	"github.com/rossedman/guardrails/internal/module"
)

func TestAskedPersistsAcrossStops(t *testing.T) {
	h := newHarness(t)
	h.handle(hook.SessionStart, h.in(hook.SessionStart, map[string]any{"source": "startup"}))
	h.touch("main.stub")

	// First Stop: the module raises a borderline requirement and blocks.
	h.mod.gate = module.GateResult{
		Checks: []feedback.Check{{Name: "bench", Failed: true, Summary: "add a benchmark"}},
		Ran:    []string{"bench"},
		Asked:  []string{"bench:pkg.Encode"},
	}
	if out := h.handle(hook.Stop, h.in(hook.Stop, nil)); !out.Blocking() {
		t.Fatalf("first stop should block: %+v", out)
	}
	if got := h.session().Asked; !got["bench:pkg.Encode"] || len(got) != 1 {
		t.Fatalf("session Asked = %v", got)
	}
	if in := h.mod.gateIn[0]; len(in.Asked) != 0 {
		t.Errorf("first gate saw Asked = %v", in.Asked)
	}

	// Second Stop: the module sees what was already asked and passes.
	h.mod.gate = module.GateResult{Ran: []string{"bench"}, Asked: []string{"fuzz:pkg.Parse"}}
	if out := h.handle(hook.Stop, h.in(hook.Stop, nil)); out.Blocking() {
		t.Fatalf("second stop should pass: %+v", out)
	}
	if in := h.mod.gateIn[1]; !in.Asked["bench:pkg.Encode"] {
		t.Errorf("second gate Asked = %v", in.Asked)
	}
	got := h.session().Asked
	if !got["bench:pkg.Encode"] || !got["fuzz:pkg.Parse"] || len(got) != 2 {
		t.Errorf("Asked should accumulate: %v", got)
	}

	// Resuming keeps what was already asked.
	h.handle(hook.SessionStart, h.in(hook.SessionStart, map[string]any{"source": "resume"}))
	if got := h.session().Asked; len(got) != 2 {
		t.Errorf("resume should keep Asked: %v", got)
	}

	// /clear drops Claude's earlier explanations, so everything is asked again.
	h.handle(hook.SessionStart, h.in(hook.SessionStart, map[string]any{"source": "clear"}))
	if got := h.session().Asked; len(got) != 0 {
		t.Errorf("clear should reset Asked: %v", got)
	}
}

func TestGateCollectsAsked(t *testing.T) {
	h := newHarness(t)
	h.mod.gate = module.GateResult{Asked: []string{"a", "b"}}
	asked := map[string]bool{"x": true}
	g := h.eng.Gate(context.Background(), h.repo, config.Default(), []string{filepath.Join(h.repo, "main.stub")}, "base", nil, false, asked)
	if len(g.Asked) != 2 || g.Asked[0] != "a" || g.Asked[1] != "b" {
		t.Errorf("GateOutcome.Asked = %v", g.Asked)
	}
	if in := h.mod.gateIn[0]; !in.Asked["x"] || in.BaseCommit != "base" {
		t.Errorf("GateInput = %+v", in)
	}
}
