package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rossedman/guardrails/internal/config"
	"github.com/rossedman/guardrails/internal/deps"
	"github.com/rossedman/guardrails/internal/feedback"
	"github.com/rossedman/guardrails/internal/guard"
	"github.com/rossedman/guardrails/internal/hook"
	"github.com/rossedman/guardrails/internal/lessons"
	"github.com/rossedman/guardrails/internal/module"
	"github.com/rossedman/guardrails/internal/runlog"
	"github.com/rossedman/guardrails/internal/state"
)

// sessionStart records the repo and base commit for the session and resets the gate on a fresh start.
// It then reports the active standards, any missing or installing tools, and pending lessons.
func (e *Engine) sessionStart(ctx context.Context, in hook.Input) (hook.Output, error) {
	repo := e.RepoRoot(ctx, in.Cwd)
	cfg, cfgErr := e.loadConfig(repo)

	base := ""
	if res, err := e.Runner.Run(ctx, repo, "git", "rev-parse", "HEAD"); err == nil && res.ExitCode == 0 {
		base = strings.TrimSpace(res.Stdout)
	}
	rc, err := e.Store.LoadRepo(repo)
	if err != nil {
		rc = &state.RepoCache{}
	}
	_, err = e.Store.Update(in.SessionID, func(s *state.Session) error {
		s.Repo = repo
		if s.Started.IsZero() {
			s.Started = e.Now()
		}
		// A fresh conversation starts a fresh gate: new base, new baseline,
		// no touched files. Resume and compact keep the session's history.
		if in.Source == "" || in.Source == "startup" || in.Source == "clear" || s.BaseCommit == "" {
			s.BaseCommit = base
			s.Baseline = copyMap(rc.Coverage)
			s.Touched, s.AgentTouched = nil, nil
			s.StopBlocks, s.AgentBlocks = 0, nil
			s.Reported = nil
		}
		return nil
	})
	if err != nil {
		return hook.Output{}, err
	}
	if cfgErr != nil {
		out := configError(cfgErr)
		out.Specific = hook.Context(hook.SessionStart, "Guardrails could not load its config, so quality checks are paused this session: "+cfgErr.Error()+". The config is owned by the human; mention this to them rather than editing it.").Specific
		return out, nil
	}

	var (
		active     []module.Module
		missing    []string
		installing []string
	)
	mgr := e.manager(cfg, false)
	active = e.ActiveModules(repo, cfg)
	for _, m := range active {
		for _, t := range m.Tools() {
			if _, err := e.Runner.LookPath(t.Name); err == nil {
				continue
			}
			if mgr != nil && mgr.Installed(t.Name) {
				continue
			}
			if mgr != nil && mgr.AutoInstall && mgr.Supported(t.Name) {
				installing = append(installing, t.Name)
				continue
			}
			kind := "optional"
			if t.Required {
				kind = "required"
			}
			missing = append(missing, fmt.Sprintf("%s (%s, %s: %s)", t.Name, m.Name(), kind, t.Install))
		}
	}
	var msgs []string
	if len(active) > 0 && !rc.Welcomed {
		// Tell the human once that Guardrails is working in this repo, and
		// where to look; after that, it speaks up only when it has to.
		msgs = append(msgs, Welcome(cfg, active))
		rc.Welcomed = true
		_ = e.Store.SaveRepo(repo, rc)
	}
	if n, _ := lessons.Pending(e.Store.Root, repo); n > 0 && cfg.LessonsEnabled() {
		msgs = append(msgs, fmt.Sprintf("%d correction(s) recorded; run /guardrails:learn to turn them into rules", n))
	}
	out := hook.Output{}
	if len(active) > 0 {
		out = hook.Context(hook.SessionStart, standardsSummary(cfg, active, rc))
	}
	if len(installing) > 0 {
		// Download pinned tools now so the first edit doesn't wait.
		if err := mgr.Prefetch(installing...); err != nil {
			msgs = append(msgs, "could not start tool install: "+err.Error())
		} else {
			msgs = append(msgs, "installing pinned tools in the background: "+strings.Join(installing, ", "))
		}
	}
	if len(missing) > 0 {
		msgs = append(msgs, "missing tools: "+strings.Join(missing, "; "))
	}
	if len(msgs) > 0 {
		out.SystemMessage = "guardrails: " + strings.Join(msgs, "; ")
	}
	return out, nil
}

// ActiveModules returns the modules that are enabled and have files in repo.
func (e *Engine) ActiveModules(repo string, cfg config.Config) []module.Module {
	var active []module.Module
	for _, m := range e.Modules {
		if m.Enabled(cfg) && present(repo, m) {
			active = append(active, m)
		}
	}
	return active
}

// DisplayName is a module's name as people write it: Go, YAML, Terraform.
func DisplayName(name string) string {
	switch name {
	case "go":
		return "Go"
	case "yaml":
		return "YAML"
	}
	if name == "" {
		return name
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

// Bar describes what a module requires, in a few words.
func Bar(cfg config.Config, name string) string {
	switch name {
	case "go":
		return coverageBar(cfg.Go.Coverage.Total, cfg.Go.PatchCoverage)
	case "python":
		return coverageBar(cfg.Python.Coverage.Total, cfg.Python.PatchCoverage)
	}
	return "lint and format"
}

// coverageBar describes a coverage bar: the total, or "may not drop" when it is 0, plus the patch percentage.
func coverageBar(total, patch float64) string {
	cov := fmt.Sprintf("%g%% coverage", total)
	if total == 0 {
		cov = "coverage may not drop"
	}
	return fmt.Sprintf("%s, %g%% of changed lines", cov, patch)
}

// Welcome is the one-time message telling the human Guardrails is on.
func Welcome(cfg config.Config, active []module.Module) string {
	var names []string
	bar := ""
	for _, m := range active {
		names = append(names, DisplayName(m.Name()))
		if b := Bar(cfg, m.Name()); bar == "" && strings.Contains(b, "changed lines") {
			bar = b
		}
	}
	preset := cfg.Preset
	if preset == "" {
		preset = config.PresetStrict
	}
	if bar != "" {
		preset += " (" + bar + ")"
	}
	return fmt.Sprintf("on in this repo for %s · %s · Claude's work is checked when it stops · /guardrails:status for details, /guardrails:setup to tune",
		strings.Join(names, ", "), preset)
}

// present reports whether the repo has files the module owns, checking the
// root and one level down so detection stays fast. Guardrails' own config
// doesn't count: it would make every repo a YAML repo.
func present(repo string, m module.Module) bool {
	for _, pattern := range []string{"*", "*/*"} {
		matches, _ := filepath.Glob(filepath.Join(repo, pattern))
		for _, p := range matches {
			if p != filepath.Join(repo, config.RepoFile) && m.Match(p) {
				return true
			}
		}
	}
	return false
}

// standardsSummary is the context shown to the agent at session start: the mode, each module's standards, and the coverage baseline.
func standardsSummary(cfg config.Config, active []module.Module, rc *state.RepoCache) string {
	var b strings.Builder
	mode := "enforced: failures block"
	if !cfg.Enforcing() {
		mode = "warn mode: failures are reported, not blocking"
	}
	fmt.Fprintf(&b, "Guardrails is active in this repo (%s).\n", mode)
	for _, m := range active {
		if line := m.Standards(cfg); line != "" {
			b.WriteString(line + "\n")
		}
	}
	b.WriteString("Suppression comments (//nolint, # noqa, ...), skipped tests, lowered thresholds and edits to guardrail config are denied; the human owns those. Write tests alongside code rather than after the gate asks.\n")
	if len(rc.Coverage) > 0 {
		var cov, n float64
		for _, pkg := range sortedKeys(rc.Coverage) {
			cov += rc.Coverage[pkg]
			n++
		}
		fmt.Fprintf(&b, "Baseline: last measured coverage averages %.1f%% across %d packages.", cov/n, int(n))
		if rc.LastGate != nil {
			fmt.Fprintf(&b, " Last gate %s with %d lint issues.", passFail(rc.LastGate.Passed), rc.LastGate.Issues)
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// passFail returns "passed" or "failed".
func passFail(ok bool) string {
	if ok {
		return "passed"
	}
	return "failed"
}

// preToolUse runs the file and Bash guards, and the dependency check, before a tool call.
// It returns a permission decision, or empty output to let the call through.
func (e *Engine) preToolUse(ctx context.Context, in hook.Input) (hook.Output, error) {
	t, err := in.Tool()
	if err != nil {
		return hook.Output{}, err
	}
	repo := e.RepoRoot(ctx, in.Cwd)
	cfg, cfgErr := e.loadConfig(repo)
	if cfgErr != nil {
		// Guards must still protect the config itself and infrastructure,
		// so fall back to defaults.
		cfg = config.Default()
	}

	var d guard.Decision
	switch in.ToolName {
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
		path := absPath(in.Cwd, t.FilePath)
		if path == "" {
			return hook.Output{}, nil
		}
		t.FilePath = path
		before, readErr := os.ReadFile(path)
		exists := readErr == nil
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return hook.Output{}, readErr
		}
		fc := guard.NewFileChange(repo, in.ToolName, t, string(before), exists)
		d = guard.CheckFile(cfg, fc)
		if d.Action != hook.Deny {
			d = guard.Worst(d, e.checkDeps(ctx, cfg, deps.FromManifest(fc.Rel, fc.Before, fc.After)))
		}
		if d.Action == hook.Allow && in.AgentType == "test-writer" && !isTestFile(path) {
			d = guard.Decision{Action: hook.Deny, Guard: "test-writer", Reason: fmt.Sprintf("the test-writer agent may only edit test files; %s is not one.", fc.Rel)}
		}
	case "Bash":
		d = guard.CheckBash(cfg, t.Command, guard.Env{
			CurrentBranch: func() string { return e.gitOutput(ctx, repo, "rev-parse", "--abbrev-ref", "HEAD") },
			KubeContext:   func() string { return e.cmdOutput(ctx, repo, "kubectl", "config", "current-context") },
		})
		if d.Action != hook.Deny {
			d = guard.Worst(d, e.checkDeps(ctx, cfg, deps.FromCommand(t.Command)))
		}
	default:
		return hook.Output{}, nil
	}
	if d.Action == hook.Allow {
		return hook.Output{}, nil
	}
	return hook.Permission(d.Action, d.Message()), nil
}

// isTestFile reports whether p is a Go or Python test file or lives under testdata.
func isTestFile(p string) bool {
	base := filepath.Base(p)
	return strings.HasSuffix(base, "_test.go") ||
		(strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py")) ||
		strings.HasSuffix(base, "_test.py") ||
		strings.Contains(filepath.ToSlash(p), "/testdata/")
}

// gitOutput runs git in dir and returns trimmed stdout, or "" on failure.
func (e *Engine) gitOutput(ctx context.Context, dir string, args ...string) string {
	return e.cmdOutput(ctx, dir, "git", args...)
}

// cmdOutput runs a command in dir and returns trimmed stdout, or "" if it fails or exits non-zero.
func (e *Engine) cmdOutput(ctx context.Context, dir, name string, args ...string) string {
	res, err := e.Runner.Run(ctx, dir, name, args...)
	if err != nil || res.ExitCode != 0 {
		return ""
	}
	return strings.TrimSpace(res.Stdout)
}

// postToolUse fixes and checks a file after an edit and records it as touched.
// Failures block the agent when enforcing, and are only reported otherwise.
func (e *Engine) postToolUse(ctx context.Context, in hook.Input) (hook.Output, error) {
	t, err := in.Tool()
	if err != nil {
		return hook.Output{}, err
	}
	path := absPath(in.Cwd, t.FilePath)
	if path == "" {
		return hook.Output{}, nil
	}
	repo := e.RepoRoot(ctx, in.Cwd)
	if !within(repo, path) {
		return hook.Output{}, nil
	}
	cfg, cfgErr := e.loadConfig(repo)
	if cfgErr != nil {
		return configError(cfgErr), nil
	}
	m := e.moduleFor(cfg, path)
	if m == nil {
		return hook.Output{}, nil
	}

	ctx, cancel := context.WithTimeout(ctx, PostToolUseBudget)
	defer cancel()
	env := e.env(repo, cfg, false)
	fix := m.Fix(ctx, env, []string{path})

	var checks []feedback.Check
	cacheKey := ""
	if content, err := os.ReadFile(path); err == nil {
		cacheKey = state.Key("post", m.Name(), path, string(content), cfg.Hash())
	}
	var clean bool
	if cacheKey == "" || !e.Store.CacheGet(cacheKey, &clean) || !clean {
		checks = m.Check(ctx, env, []string{path})
		if len(checks) == 0 && cacheKey != "" {
			_ = e.Store.CachePut(cacheKey, true)
		}
	}

	sess, err := e.Store.Load(in.SessionID)
	if err != nil {
		return hook.Output{}, err
	}
	out := hook.Output{SystemMessage: notes(fix.Notes)}
	var rendered feedback.Rendered
	if len(checks) > 0 {
		rendered = feedback.Render(feedback.Report{
			Title:  fmt.Sprintf("guardrails: %s check failed for %s", m.Name(), displayRel(repo, path)),
			Checks: checks,
		}, feedback.Options{
			Seen:         sess.Reported,
			OverflowPath: filepath.Join(e.Store.SessionDir(in.SessionID), "last-edit-report.txt"),
		})
		if rendered.Truncated {
			_ = writeFile(filepath.Join(e.Store.SessionDir(in.SessionID), "last-edit-report.txt"), rendered.Full)
		}
		if cfg.Enforcing() {
			out.Decision, out.Reason = "block", rendered.Text
		} else {
			out.SystemMessage = strings.TrimSpace(out.SystemMessage + "\n" + rendered.Text)
		}
	}
	_, err = e.Store.Update(in.SessionID, func(s *state.Session) error {
		if s.Repo == "" {
			s.Repo = repo
		}
		s.Touch(in.AgentID, path)
		if s.Reported == nil {
			s.Reported = map[string]bool{}
		}
		for _, fp := range rendered.New {
			s.Reported[fp] = true
		}
		return nil
	})
	return out, err
}

// stop runs the gate over the files this agent touched when it tries to finish.
// When enforcing, a failed gate blocks the stop until the block limit is reached, then hands off.
func (e *Engine) stop(ctx context.Context, in hook.Input, agent string) (hook.Output, error) {
	started := e.Now()
	sess, err := e.Store.Load(in.SessionID)
	if err != nil {
		return hook.Output{}, err
	}
	files := sess.TouchedFiles(agent)
	if len(files) == 0 {
		return hook.Output{}, nil
	}
	repo := sess.Repo
	if repo == "" {
		repo = e.RepoRoot(ctx, in.Cwd)
	}
	cfg, cfgErr := e.loadConfig(repo)
	if cfgErr != nil {
		return configError(cfgErr), nil
	}

	g := e.Gate(ctx, repo, cfg, files, sess.BaseCommit, sess.Baseline, false)
	if len(g.Modules) == 0 {
		return hook.Output{}, nil
	}
	checks, cov, total, names := g.Checks, g.Coverage, g.Total, g.Modules

	report := feedback.Report{Checks: checks}
	failed := report.Failed()
	passed := len(failed) == 0
	overflow := filepath.Join(e.Store.SessionDir(in.SessionID), "last-gate-report.txt")

	var out hook.Output
	var attempt int
	handedOff := false
	_, err = e.Store.Update(in.SessionID, func(s *state.Session) error {
		s.Gates++
		if passed {
			s.SetBlocks(agent, 0)
			// The ratchet only moves up.
			if s.Baseline == nil {
				s.Baseline = map[string]float64{}
			}
			for pkg, pct := range cov {
				if pct > s.Baseline[pkg] {
					s.Baseline[pkg] = pct
				}
			}
			return nil
		}
		attempt = s.Blocks(agent) + 1
		if attempt > cfg.Gate.MaxConsecutiveBlocks {
			handedOff = true
			s.HandedOff++
			s.SetBlocks(agent, 0)
			return nil
		}
		s.Blocked++
		s.SetBlocks(agent, attempt)
		return nil
	})
	if err != nil {
		return hook.Output{}, err
	}

	if !passed {
		report.Title = fmt.Sprintf("guardrails: %s gate failed (%d of %d checks)", strings.Join(names, "+"), len(failed), max(g.Ran, len(failed)))
		report.Footer = fmt.Sprintf("attempt %d of %d — run `guardrails gate --explain` for full output", attempt, cfg.Gate.MaxConsecutiveBlocks)
		rendered := feedback.Render(report, feedback.Options{OverflowPath: overflow})
		_ = writeFile(overflow, rendered.Full)
		switch {
		case !cfg.Enforcing():
			out.SystemMessage = rendered.Text
		case handedOff:
			report.Footer = fmt.Sprintf("still failing after %d attempts; handing off to you", cfg.Gate.MaxConsecutiveBlocks)
			out.SystemMessage = feedback.Render(report, feedback.Options{OverflowPath: overflow}).Text
		default:
			out = hook.Block(rendered.Text)
		}
	}
	if n := notes(g.Notes); n != "" {
		out.SystemMessage = strings.TrimSpace(out.SystemMessage + "\n" + n)
	}

	e.RecordGate(repo, overflow, passed, total, cov, cfg, checks)
	_ = runlog.Append(e.Store.Root, runlog.Record{
		Type: "gate", At: e.Now(), Session: in.SessionID, Repo: repo,
		Event: in.HookEventName, AgentType: in.AgentType, Passed: passed,
		Attempt: attempt, HandedOff: handedOff, Failures: failures(checks),
		Coverage: total, Modules: names, Millis: e.Now().Sub(started).Milliseconds(),
	})
	return out, nil
}

// RecordGate updates the repo cache. Coverage is only recorded from passing
// gates, so the next session's ratchet baseline is a state the gate accepted.
func (e *Engine) RecordGate(repo, reportPath string, passed bool, total float64, cov map[string]float64, cfg config.Config, checks []feedback.Check) {
	rc, err := e.Store.LoadRepo(repo)
	if err != nil {
		rc = &state.RepoCache{}
	}
	if rc.Coverage == nil {
		rc.Coverage = map[string]float64{}
	}
	if passed {
		for pkg, pct := range cov {
			rc.Coverage[pkg] = pct
		}
	}
	rc.LastGate = &state.GateSummary{
		Passed: passed, Coverage: total, Threshold: cfg.Go.Coverage.Total,
		Issues: countIssues(checks), At: e.Now(),
	}
	rc.LastReport = reportPath
	_ = e.Store.SaveRepo(repo, rc)
}

// sessionEnd appends a summary record of the session to the run log.
func (e *Engine) sessionEnd(in hook.Input) (hook.Output, error) {
	sess, err := e.Store.Load(in.SessionID)
	if err != nil {
		return hook.Output{}, err
	}
	err = runlog.Append(e.Store.Root, runlog.Record{
		Type: "session", At: e.Now(), Session: in.SessionID, Repo: sess.Repo,
		Reason: in.Reason, Gates: sess.Gates, Blocked: sess.Blocked,
		HandedOff: sess.HandedOff > 0, Touched: len(sess.Touched),
		Millis: e.Now().Sub(sess.Started).Milliseconds(),
	})
	return hook.Output{}, err
}

// displayRel returns p relative to repo with forward slashes, or p unchanged if that fails.
func displayRel(repo, p string) string {
	if rel, err := filepath.Rel(repo, p); err == nil {
		return filepath.ToSlash(rel)
	}
	return p
}

// writeFile writes content to path, creating its directory. Only the owner can read it.
func writeFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o600)
}

// copyMap returns a copy of m, keeping nil as nil.
func copyMap(m map[string]float64) map[string]float64 {
	if m == nil {
		return nil
	}
	out := make(map[string]float64, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
