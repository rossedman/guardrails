package engine

import (
	"context"

	"github.com/rossedman/guardrails/internal/config"
	"github.com/rossedman/guardrails/internal/hook"
	"github.com/rossedman/guardrails/internal/lessons"
	"github.com/rossedman/guardrails/internal/state"
)

// userPromptSubmit records prompts that correct Claude as lessons, so
// /guardrails:learn can turn them into rules. It never blocks a prompt.
func (e *Engine) userPromptSubmit(ctx context.Context, in hook.Input) (hook.Output, error) {
	repo := e.RepoRoot(ctx, in.Cwd)
	cfg, on := e.lessonsConfig(repo)
	if !on {
		return hook.Output{}, nil
	}
	prior := 0
	if _, err := e.Store.Update(in.SessionID, func(s *state.Session) error {
		prior = s.Prompts
		s.Prompts++
		return nil
	}); err != nil {
		return hook.Output{}, err
	}
	signals, explicit, ok := lessons.Detect(in.Prompt)
	// A correction needs something to correct: the first prompt of a
	// session counts only when it states a standing rule.
	if !ok || (prior == 0 && !explicit) {
		return hook.Output{}, nil
	}
	l := lessons.Lesson{Time: e.Now(), Session: in.SessionID, Repo: repo, Prompt: in.Prompt, Signals: signals, Explicit: explicit}
	if err := lessons.Record(e.Store.Root, l); err != nil {
		return hook.Output{}, err
	}
	if !cfg.Lessons.Nudge {
		return hook.Output{}, nil
	}
	return hook.Context(hook.UserPromptSubmit, lessons.Nudge(l)), nil
}

// lessonsConfig loads the config and reports whether lessons are captured.
// A config that fails to load turns capture off: SessionStart already told
// the user, and a prompt is never blocked over it.
func (e *Engine) lessonsConfig(repo string) (config.Config, bool) {
	cfg, err := e.loadConfig(repo)
	return cfg, err == nil && cfg.LessonsEnabled()
}
