package cli

import (
	"github.com/spf13/cobra"

	"github.com/rossedman/guardrails/internal/config"
)

func newSchemaCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "schema",
		Short: "Print the config JSON Schema",
		Long: `Print the JSON Schema for .guardrails.yaml and the user-wide config, for
editor completion and validation.`,
		Example: `  guardrails schema > .guardrails.schema.json`,
		Args:    usageArgs(cobra.NoArgs),
		RunE: func(*cobra.Command, []string) error {
			_, err := a.Stdout.Write(config.Schema)
			return err
		},
	}
}
