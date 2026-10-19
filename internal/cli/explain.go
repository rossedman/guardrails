package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/rossedman/guardrails/internal/state"
)

func newExplainCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "explain",
		Short: "Print the full report from the last gate in this repo",
		Long: `Print the full, untruncated report from the last gate that ran in this
repository, whether Claude's Stop hook or "guardrails gate" ran it.

Claude sees a short report capped to keep its context small; this is
everything behind it. Same as "guardrails gate --explain" without re-running
the gate.`,
		Example: `  guardrails explain | less`,
		Hidden:  true,
		Args:    usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, err := a.repo(cmd.Context())
			if err != nil {
				return err
			}
			rc, err := state.New(a.DataDir()).LoadRepo(repo)
			if err != nil {
				return err
			}
			if rc.LastReport == "" {
				return errors.New("no gate has run in this repo yet")
			}
			data, err := os.ReadFile(rc.LastReport)
			if err != nil {
				return fmt.Errorf("read last report: %w", err)
			}
			_, err = a.Stdout.Write(data)
			return err
		},
	}
}
