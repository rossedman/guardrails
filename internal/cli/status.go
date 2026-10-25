package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/rossedman/guardrails/internal/config"
	"github.com/rossedman/guardrails/internal/engine"
	"github.com/rossedman/guardrails/internal/lessons"
	"github.com/rossedman/guardrails/internal/state"
)

// statusReport is everything "guardrails status" shows, also its JSON.
type statusReport struct {
	Version   string           `json:"version"`
	Command   string           `json:"command"`
	OnPath    bool             `json:"on_path"`
	Repo      string           `json:"repo"`
	Config    statusConfig     `json:"config"`
	Languages []statusLanguage `json:"languages"`
	LastGate  *statusGate      `json:"last_gate,omitempty"`
	Lessons   int              `json:"lessons_waiting"`
	Deps      bool             `json:"dependency_check"`
	Next      string           `json:"next"`
}

type statusConfig struct {
	Preset string   `json:"preset"`
	Mode   string   `json:"mode"`
	Files  []string `json:"files"`
	Error  string   `json:"error,omitempty"`
}

type statusLanguage struct {
	Name         string   `json:"name"`
	Bar          string   `json:"bar"`
	MissingTools []string `json:"missing_tools,omitempty"`
}

type statusGate struct {
	Passed    bool      `json:"passed"`
	Coverage  float64   `json:"coverage"`
	Threshold float64   `json:"threshold"`
	Issues    int       `json:"issues"`
	At        time.Time `json:"at"`
	Report    string    `json:"report,omitempty"`
}

// newStatusCmd builds the status command.
func newStatusCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "What guardrails is doing in this repo, and what to do next",
		Long: `Show what guardrails does in the repository you're in: the preset and
config files that apply, the languages it gates and the bar for each, how
the last gate went, corrections waiting to become rules, and the one thing
worth doing next.

In Claude Code, /guardrails:status shows the same.`,
		Example: `  guardrails status
  guardrails status --json`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			rep, err := a.status(cmd)
			if err != nil {
				return err
			}
			if a.V.GetBool("status.json") {
				return writeJSON(a.Stdout, rep)
			}
			printStatus(a.Stdout, rep, a.Now())
			return nil
		},
	}
	cmd.Flags().Bool("json", false, "print the status as JSON")
	a.bind(cmd, "status")
	return cmd
}

// status gathers what guardrails is doing in the current repo: the binary,
// the languages and their bar, the config, the last gate and pending corrections.
func (a *App) status(cmd *cobra.Command) (statusReport, error) {
	repo, err := a.repo(cmd.Context())
	if err != nil {
		return statusReport{}, err
	}
	rep := statusReport{Version: Version, Command: a.launcher(), Repo: repo, Languages: []statusLanguage{}}
	if _, err := a.Runner.LookPath("guardrails"); err == nil {
		rep.OnPath = true
	}

	cfg, cfgErr := config.Load(a.globalConfig(), repo)
	if cfgErr != nil {
		rep.Config.Error = cfgErr.Error()
		cfg = config.Default()
	}
	rep.Config.Preset = cfg.Preset
	if rep.Config.Preset == "" {
		rep.Config.Preset = config.PresetStrict
	}
	rep.Config.Mode = cfg.Mode
	rep.Config.Files = []string{}
	repoFile := filepath.Join(repo, config.RepoFile)
	for _, p := range []string{a.globalConfig(), repoFile} {
		if _, err := os.Stat(p); p != "" && err == nil {
			rep.Config.Files = append(rep.Config.Files, p)
		}
	}
	rep.Deps = cfg.DepsEnabled()

	mgr := a.toolManager()
	if mgr != nil {
		mgr.Prefer = cfg.Tools.Prefer
		mgr.AutoInstall = cfg.Tools.Install != config.InstallNever
	}
	missing := false
	for _, m := range a.engine().ActiveModules(repo, cfg) {
		lang := statusLanguage{Name: engine.DisplayName(m.Name()), Bar: engine.Bar(cfg, m.Name())}
		for _, t := range m.Tools() {
			if !t.Required {
				continue
			}
			if path, _ := a.locateTool(mgr, t.Name); path != "" || (mgr != nil && mgr.AutoInstall && mgr.Supported(t.Name)) {
				continue
			}
			lang.MissingTools = append(lang.MissingTools, t.Name)
			missing = true
		}
		rep.Languages = append(rep.Languages, lang)
	}

	if rc, err := state.New(a.DataDir()).LoadRepo(repo); err == nil && rc.LastGate != nil {
		g := rc.LastGate
		rep.LastGate = &statusGate{Passed: g.Passed, Coverage: g.Coverage, Threshold: g.Threshold, Issues: g.Issues, At: g.At, Report: rc.LastReport}
	}
	if cfg.LessonsEnabled() {
		rep.Lessons, _ = lessons.Pending(a.DataDir(), repo)
	}

	_, statErr := os.Stat(repoFile)
	switch {
	case cfgErr != nil:
		rep.Next = "fix the config error above (guardrails config shows what loads)"
	case statErr != nil:
		rep.Next = "run /guardrails:setup in Claude Code to pick a preset for this repo (or: guardrails init --preset adopt)"
	case rep.LastGate != nil && !rep.LastGate.Passed:
		rep.Next = "see why the last gate failed: guardrails gate --explain"
	case rep.Lessons > 0:
		rep.Next = fmt.Sprintf("turn %d recorded correction(s) into rules: /guardrails:learn", rep.Lessons)
	case missing:
		rep.Next = "install the missing tools: guardrails tools install"
	default:
		rep.Next = "nothing to do: Claude's work is gated when it stops"
	}
	return rep, nil
}

// printStatus writes rep as the human-readable status report, showing the
// last gate's age relative to now.
func printStatus(w io.Writer, rep statusReport, now time.Time) {
	fmt.Fprintf(w, "guardrails %s\n", rep.Version)
	fmt.Fprintf(w, "  command   %s\n", rep.Command)
	if !rep.OnPath {
		fmt.Fprintf(w, "            not on your PATH; to run it from a terminal:\n            ln -s %s ~/.local/bin/guardrails\n", rep.Command)
	}
	fmt.Fprintf(w, "  repo      %s\n", rep.Repo)

	switch {
	case rep.Config.Error != "":
		fmt.Fprintf(w, "  config    ✗ %s\n", rep.Config.Error)
	case len(rep.Config.Files) == 0:
		fmt.Fprintf(w, "  config    ○ defaults: %s (no .guardrails.yaml)\n", config.PresetSummary(rep.Config.Preset))
	default:
		fmt.Fprintf(w, "  config    ✓ %s\n", config.PresetSummary(rep.Config.Preset))
		for _, f := range rep.Config.Files {
			fmt.Fprintf(w, "            %s\n", f)
		}
	}
	mode := "enforce: failures block Claude until fixed"
	if rep.Config.Mode == config.ModeWarn {
		mode = "warn: failures are reported, never blocked"
	}
	fmt.Fprintf(w, "  mode      %s\n", mode)

	fmt.Fprintf(w, "\nLanguages\n")
	if len(rep.Languages) == 0 {
		fmt.Fprintf(w, "  ○ none found here; guardrails gates Go, Python, Shell, YAML, Terraform and Docker\n")
	}
	for _, l := range rep.Languages {
		mark := "✓"
		if len(l.MissingTools) > 0 {
			mark = "✗"
		}
		fmt.Fprintf(w, "  %s %-10s %s\n", mark, l.Name, l.Bar)
		for _, t := range l.MissingTools {
			fmt.Fprintf(w, "               missing %s\n", t)
		}
	}

	fmt.Fprintf(w, "\nLast gate\n")
	if g := rep.LastGate; g == nil {
		fmt.Fprintf(w, "  ○ none yet: it runs when Claude stops, or with guardrails gate\n")
	} else {
		mark, result := "✓", "passed"
		if !g.Passed {
			mark, result = "✗", "failed"
		}
		cov := "coverage n/a"
		if g.Coverage >= 0 {
			cov = fmt.Sprintf("coverage %.1f%% (bar %.0f%%)", g.Coverage, g.Threshold)
		}
		fmt.Fprintf(w, "  %s %s %s · %s · %d lint issue(s)\n", mark, result, ago(now.Sub(g.At)), cov, g.Issues)
		if g.Report != "" {
			fmt.Fprintf(w, "    report: %s\n", g.Report)
		}
	}

	fmt.Fprintf(w, "\nAlso\n")
	fmt.Fprintf(w, "  lessons   %d correction(s) waiting to become rules\n", rep.Lessons)
	deps := "off"
	if rep.Deps {
		deps = "on: packages are looked up before they're installed"
	}
	fmt.Fprintf(w, "  deps      %s\n", deps)
	fmt.Fprintf(w, "\nNext: %s\n", rep.Next)
}

// ago renders a duration as "just now", "5m ago", "3h ago" or "2d ago".
func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
