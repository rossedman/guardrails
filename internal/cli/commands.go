package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/rossedman/guardrails/internal/assets"
	"github.com/rossedman/guardrails/internal/config"
	"github.com/rossedman/guardrails/internal/engine"
	"github.com/rossedman/guardrails/internal/feedback"
	"github.com/rossedman/guardrails/internal/hook"
	"github.com/rossedman/guardrails/internal/state"
)

// hook handles `guardrails hook <event>`. Internal errors on events that
// can block exit 2 so the action fails closed; on other events they exit 1,
// which Claude Code reports without blocking.
func (a *App) hook(ctx context.Context, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(a.Stderr, "usage: guardrails hook <SessionStart|PreToolUse|PostToolUse|Stop|SubagentStop|SessionEnd>")
		return 2
	}
	event, ok := engine.NormalizeEvent(args[0])
	if !ok {
		fmt.Fprintf(a.Stderr, "guardrails: unknown hook event %q\n", args[0])
		return 2
	}
	fail := func(err error) int {
		fmt.Fprintf(a.Stderr, "guardrails %s: %v\n", event, err)
		if engine.Blocking(event) {
			return 2
		}
		return 1
	}
	in, err := hook.ReadInput(a.Stdin)
	if err != nil {
		return fail(err)
	}
	out, err := a.engine().Handle(ctx, event, in)
	if err != nil {
		return fail(err)
	}
	if err := out.Write(a.Stdout); err != nil {
		return fail(err)
	}
	return 0
}

// gate handles `guardrails gate`.
func (a *App) gate(ctx context.Context, args []string) int {
	fl := newFlags("gate", a.Stderr)
	all := fl.Bool("all", false, "gate every package, not just changed files")
	base := fl.String("base", "HEAD", "git ref that changes and patch coverage are measured against")
	explain := fl.Bool("explain", false, "print the full, untruncated report")
	asJSON := fl.Bool("json", false, "print checks as JSON")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	e := a.engine()
	wd, err := a.Getwd()
	if err != nil {
		fmt.Fprintf(a.Stderr, "guardrails: %v\n", err)
		return 1
	}
	repo := e.RepoRoot(ctx, wd)
	cfg, err := config.Load(a.globalConfig(), repo)
	if err != nil {
		fmt.Fprintf(a.Stderr, "guardrails: %v\n", err)
		return 1
	}
	var files []string
	if !*all {
		files = a.changedFiles(ctx, repo, *base)
		if len(files) == 0 {
			fmt.Fprintf(a.Stdout, "guardrails: no changes against %s; use --all to gate everything\n", *base)
			return 0
		}
	}
	rc, _ := e.Store.LoadRepo(repo)
	var baseline map[string]float64
	if rc != nil {
		baseline = rc.Coverage
	}
	patchBase := ""
	if !*all {
		patchBase = *base
	}
	g := e.Gate(ctx, repo, cfg, files, patchBase, baseline, *all)
	report := feedback.Report{Checks: g.Checks}
	failed := report.Failed()
	passed := len(failed) == 0

	reportPath := filepath.Join(a.DataDir(), "reports", state.Key(repo)+".txt")
	report.Title = fmt.Sprintf("guardrails: %s gate %s", strings.Join(g.Modules, "+"), passFail(passed, len(failed), g.Ran))
	rendered := feedback.Render(report, feedback.Options{OverflowPath: reportPath})
	_ = os.MkdirAll(filepath.Dir(reportPath), 0o700)
	_ = os.WriteFile(reportPath, []byte(rendered.Full), 0o600)
	e.RecordGate(repo, reportPath, passed, g.Total, g.Coverage, cfg, g.Checks)

	switch {
	case *asJSON:
		enc := json.NewEncoder(a.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(map[string]any{"passed": passed, "coverage": g.Total, "checks": failed, "notes": g.Notes})
	case *explain:
		fmt.Fprint(a.Stdout, rendered.Full)
	default:
		fmt.Fprint(a.Stdout, rendered.Text)
	}
	if !*asJSON {
		for _, n := range g.Notes {
			fmt.Fprintln(a.Stderr, "note: "+n)
		}
		if passed && g.Total >= 0 {
			fmt.Fprintf(a.Stdout, "coverage %.1f%% (threshold %.0f%%)\n", g.Total, cfg.Go.Coverage.Total)
		}
	}
	if !passed {
		return 1
	}
	return 0
}

func passFail(passed bool, failed, ran int) string {
	if passed {
		return fmt.Sprintf("passed (%d checks)", ran)
	}
	return fmt.Sprintf("failed (%d of %d checks)", failed, max(ran, failed))
}

// changedFiles lists files changed against base plus untracked files, as
// absolute paths.
func (a *App) changedFiles(ctx context.Context, repo, base string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(stdout string) {
		for _, rel := range trimLines(stdout) {
			p := filepath.Join(repo, filepath.FromSlash(rel))
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	if res, err := a.Runner.Run(ctx, repo, "git", "diff", "--name-only", "--diff-filter=d", base); err == nil && res.ExitCode == 0 {
		add(res.Stdout)
	}
	if res, err := a.Runner.Run(ctx, repo, "git", "ls-files", "--others", "--exclude-standard"); err == nil && res.ExitCode == 0 {
		add(res.Stdout)
	}
	return out
}

// doctor handles `guardrails doctor`.
func (a *App) doctor(ctx context.Context) int {
	e := a.engine()
	missingRequired := false
	fmt.Fprintf(a.Stdout, "guardrails %s\n", Version)
	fmt.Fprintf(a.Stdout, "data dir: %s\n", a.DataDir())
	if repo, err := a.repo(ctx); err == nil {
		if _, err := config.Load(a.globalConfig(), repo); err != nil {
			fmt.Fprintf(a.Stdout, "config:   ✗ %v\n", err)
			missingRequired = true
		} else {
			fmt.Fprintf(a.Stdout, "config:   ✓ %s\n", repo)
		}
	}
	for _, m := range e.Modules {
		fmt.Fprintf(a.Stdout, "\n%s module\n", m.Name())
		for _, t := range m.Tools() {
			path, err := a.Runner.LookPath(t.Name)
			kind := "optional"
			if t.Required {
				kind = "required"
			}
			if err != nil {
				if t.Required {
					missingRequired = true
				}
				fmt.Fprintf(a.Stdout, "  ✗ %-14s %s, not found — %s\n", t.Name, kind, t.Install)
				continue
			}
			version := ""
			if len(t.VersionArgs) > 0 {
				if res, err := a.Runner.Run(ctx, "", t.Name, t.VersionArgs...); err == nil {
					if ls := trimLines(res.Combined()); len(ls) > 0 {
						version = ls[0]
					}
				}
			}
			fmt.Fprintf(a.Stdout, "  ✓ %-14s %s %s\n", t.Name, path, version)
		}
	}
	if missingRequired {
		return 1
	}
	return 0
}

// init handles `guardrails init`.
func (a *App) init(args []string) error {
	fl := newFlags("init", a.Stderr)
	force := fl.Bool("force", false, "overwrite existing files")
	rules := fl.Bool("rules", true, "install path-scoped rules into .claude/rules/")
	if err := fl.Parse(args); err != nil {
		return err
	}
	ctx := context.Background()
	repo, err := a.repo(ctx)
	if err != nil {
		return err
	}
	write := func(rel string, data []byte) error {
		p := filepath.Join(repo, rel)
		if _, err := os.Stat(p); err == nil && !*force {
			fmt.Fprintf(a.Stdout, "  skip   %s (exists; --force to overwrite)\n", rel)
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			return err
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			return err
		}
		fmt.Fprintf(a.Stdout, "  write  %s\n", rel)
		return nil
	}
	tmpl, err := fs.ReadFile(assets.FS, "guardrails.yaml")
	if err != nil {
		return err
	}
	if err := write(config.RepoFile, tmpl); err != nil {
		return err
	}
	if *rules {
		err := fs.WalkDir(assets.FS, "rules", func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, err := fs.ReadFile(assets.FS, p)
			if err != nil {
				return err
			}
			return write(filepath.Join(".claude", p), data)
		})
		if err != nil {
			return err
		}
	}
	snippet, err := fs.ReadFile(assets.FS, "settings.json")
	if err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "\nPlugins cannot ship permission rules. For hard denies that hold even if a\nhook fails, merge this into .claude/settings.json:\n\n%s", snippet)
	return nil
}

// statusline prints one line for Claude Code's statusLine. It never fails:
// a status line must not break the UI.
func (a *App) statusline(ctx context.Context) {
	var in struct {
		Cwd       string `json:"cwd"`
		Workspace struct {
			CurrentDir string `json:"current_dir"`
		} `json:"workspace"`
	}
	if a.Stdin != nil {
		_ = json.NewDecoder(io.LimitReader(a.Stdin, 1<<20)).Decode(&in)
	}
	dir := in.Workspace.CurrentDir
	if dir == "" {
		dir = in.Cwd
	}
	if dir == "" {
		dir, _ = a.Getwd()
	}
	repo := a.engine().RepoRoot(ctx, dir)
	rc, err := state.New(a.DataDir()).LoadRepo(repo)
	if err != nil || rc.LastGate == nil {
		fmt.Fprintln(a.Stdout, "⛨ guardrails: no gate yet")
		return
	}
	g := rc.LastGate
	mark := "✓"
	if !g.Passed {
		mark = "✗"
	}
	cov := "cov n/a"
	if g.Coverage >= 0 {
		cov = fmt.Sprintf("cov %.1f%%/%.0f%%", g.Coverage, g.Threshold)
	}
	fmt.Fprintf(a.Stdout, "⛨ gate %s · %s · lint %d\n", mark, cov, g.Issues)
}

func printYAML(w io.Writer, v any) error {
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return err
	}
	return enc.Close()
}
