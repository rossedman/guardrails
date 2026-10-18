package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/rossedman/guardrails/internal/tools"
)

func newToolsListCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Show every tool, its pinned version and where it resolves from",
		Long: `List every tool guardrails knows, its pinned version and source (a release
archive, PyPI, uv or go install), and what it resolves to now: a binary on
your PATH, the managed copy, or not installed yet.`,
		Example: `  guardrails tools list`,
		Args:    usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			m, err := a.managedTools(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Fprintf(a.Stdout, "%-14s %-10s %-8s %s\n", "TOOL", "PINNED", "SOURCE", "RESOLVES TO")
			for _, name := range m.Lock.Names() {
				t := m.Lock.Tools[name]
				where := "not installed"
				switch {
				case m.Prefer != tools.PreferManaged && onPath(m, name) != "":
					where = onPath(m, name) + " (PATH)"
				case m.Installed(name):
					where = "managed"
				case !m.Supported(name):
					where = "unsupported on " + tools.Platform()
				case onPath(m, name) != "":
					where = onPath(m, name) + " (PATH)"
				}
				fmt.Fprintf(a.Stdout, "%-14s %-10s %-8s %s\n", name, t.Version, t.Source, where)
			}
			return nil
		},
	}
}

func onPath(m *tools.Manager, name string) string {
	p, err := m.LookPath(name)
	if err != nil {
		return ""
	}
	return p
}
