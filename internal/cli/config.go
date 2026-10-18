package cli

import (
	"github.com/spf13/cobra"

	"github.com/rossedman/guardrails/internal/config"
)

func newConfigCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "config",
		Short: "Print the effective configuration",
		Long: `Print the configuration guardrails uses in this repository, as YAML: the
defaults, overlaid by the user-wide file (~/.config/guardrails/config.yaml
or --config), overlaid by the repo's .guardrails.yaml.

It fails if either file is invalid, naming every problem.`,
		Example: `  guardrails config
  guardrails config --config ./team.yaml`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, err := a.repo(cmd.Context())
			if err != nil {
				return err
			}
			cfg, err := config.Load(a.globalConfig(), repo)
			if err != nil {
				return err
			}
			return printYAML(a.Stdout, cfg)
		},
	}
}
