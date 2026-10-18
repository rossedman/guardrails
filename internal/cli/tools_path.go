package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newToolsPathCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "path <tool>",
		Short: "Print the command used to run a tool, installing it if needed",
		Long: `Print the command guardrails runs for a tool, resolving it the way hooks
do and installing the pinned version if it's missing. Tools run through an
interpreter print the interpreter and its arguments too.`,
		Example: `  guardrails tools path ruff
  $(guardrails tools path shellcheck) scripts/*.sh`,
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := a.managedTools(cmd.Context())
			if err != nil {
				return err
			}
			c, err := m.WithBlocking(true).Resolve(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fmt.Fprintln(a.Stdout, strings.Join(append([]string{c.Path}, c.Args...), " "))
			return nil
		},
	}
}
