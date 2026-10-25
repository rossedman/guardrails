package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newVersionCmd builds the version command.
func newVersionCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Long: `Print the guardrails version, the commit it was built from and when.

Release builds carry their tag; local builds report "dev". The plugin's
launcher downloads the release matching .claude-plugin/plugin.json.`,
		Example: `  guardrails version
  guardrails --version`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(*cobra.Command, []string) error {
			fmt.Fprintf(a.Stdout, "guardrails %s (commit %s, built %s)\n", Version, Commit, Date)
			return nil
		},
	}
}
