package testdepth

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/rossedman/guardrails/internal/feedback"
)

func TestResultMerge(t *testing.T) {
	t.Parallel()
	r := Result{
		Checks: []feedback.Check{{Name: "a"}},
		Notes:  []string{"n1"},
		Ran:    []string{"mutation"},
	}
	r.Merge(Result{
		Checks: []feedback.Check{{Name: "b"}},
		Notes:  []string{"n2"},
		Ran:    []string{"bench"},
	})
	r.Merge(Result{})
	if len(r.Checks) != 2 || r.Checks[1].Name != "b" {
		t.Errorf("Checks = %+v", r.Checks)
	}
	if !slices.Equal(r.Notes, []string{"n1", "n2"}) || !slices.Equal(r.Ran, []string{"mutation", "bench"}) {
		t.Errorf("merged = %+v", r)
	}
}

func TestSlice(t *testing.T) {
	t.Parallel()
	ctx, cancel := Slice(context.Background(), time.Hour)
	defer cancel()
	dl, ok := ctx.Deadline()
	if !ok || time.Until(dl) > time.Hour || time.Until(dl) < 59*time.Minute {
		t.Errorf("deadline = %v, %v", dl, ok)
	}

	// No budget: no deadline of its own, still cancellable.
	ctx, cancel = Slice(context.Background(), 0)
	if _, ok := ctx.Deadline(); ok {
		t.Error("zero budget should not set a deadline")
	}
	cancel()
	if ctx.Err() == nil {
		t.Error("cancel did not cancel")
	}

	// A parent deadline earlier than the budget wins.
	parent, pcancel := context.WithTimeout(context.Background(), time.Minute)
	defer pcancel()
	ctx, cancel = Slice(parent, time.Hour)
	defer cancel()
	if dl, _ := ctx.Deadline(); time.Until(dl) > time.Minute {
		t.Errorf("parent deadline not honored: %v", dl)
	}
}
