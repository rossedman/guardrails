package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/rossedman/guardrails/internal/state"
)

func newStatuslineCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "statusline",
		Short: "Print a one-line status for Claude Code's status line",
		Long: `Print one line with the last gate's result, coverage against its threshold,
and lint issue count, for Claude Code's statusLine setting. It reads Claude
Code's status JSON on stdin to find the workspace.

It never fails: a status line must not break the UI.`,
		Example: `  # In ~/.claude/settings.json
  "statusLine": {"type": "command", "command": "guardrails statusline"}`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
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
			repo := a.engine().RepoRoot(cmd.Context(), dir)
			g := lastGate(a.DataDir(), repo)
			if g == nil {
				fmt.Fprintln(a.Stdout, "⛨ guardrails: no gate yet")
				return nil
			}
			mark := "✓"
			if !g.Passed {
				mark = "✗"
			}
			cov := "cov n/a"
			if g.Coverage >= 0 {
				cov = fmt.Sprintf("cov %.1f%%/%.0f%%", g.Coverage, g.Threshold)
			}
			fmt.Fprintf(a.Stdout, "⛨ gate %s · %s · lint %d\n", mark, cov, g.Issues)
			return nil
		},
	}
}

// lastGate is the repo's last gate result, or nil when there is none or the
// state can't be read.
func lastGate(dataDir, repo string) *state.GateSummary {
	rc, err := state.New(dataDir).LoadRepo(repo)
	if err != nil {
		return nil
	}
	return rc.LastGate
}
