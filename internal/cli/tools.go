package cli

import (
	"context"
	"errors"

	"github.com/spf13/cobra"

	"github.com/rossedman/guardrails/internal/config"
	"github.com/rossedman/guardrails/internal/tools"
)

// newToolsCmd builds the tools parent command; run alone it prints help and exits 2.
func newToolsCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tools",
		Short: "List, install or locate the external tools Guardrails runs",
		Long: `Manage the linters, formatters and scanners the language modules run.

Each tool is pinned in tools.yaml and checksum-locked per platform. A tool
on your PATH is used first (unless tools.prefer is managed); otherwise
guardrails installs the pinned version into its data dir on first use, so
the host needs nothing but the plugin.`,
		Example: `  guardrails tools list
  guardrails tools install            # everything, e.g. in CI or a Dockerfile
  guardrails tools install ruff shellcheck
  guardrails tools path golangci-lint`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.helpAndFail(cmd)
		},
	}
	cmd.AddCommand(newToolsListCmd(a), newToolsInstallCmd(a), newToolsPathCmd(a))
	return cmd
}

// managedTools returns the tool manager, honoring the repo's tools.prefer.
func (a *App) managedTools(ctx context.Context) (*tools.Manager, error) {
	m := a.toolManager()
	if m == nil {
		return nil, errors.New("tool provisioning is unavailable")
	}
	if repo, err := a.repo(ctx); err == nil {
		if cfg, err := config.Load(a.globalConfig(), repo); err == nil {
			m.Prefer = cfg.Tools.Prefer
		}
	}
	return m, nil
}
