package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newToolsInstallCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install [tool...]",
		Short: "Install pinned tools (all supported tools when none are named)",
		Long: `Download, verify and install the pinned version of each named tool into the
data dir, or of every tool supported on this platform when none are named.
Tools already installed are skipped.

Hooks install tools in the background on first use; run this to do it up
front, as the Stop hook and CI do.`,
		Example: `  guardrails tools install
  guardrails tools install --quiet golangci-lint gofumpt`,
		ValidArgsFunction: func(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
			m := a.toolManager()
			if m == nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return m.Lock.Names(), cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, names []string) error {
			ctx := cmd.Context()
			m, err := a.managedTools(ctx)
			if err != nil {
				return err
			}
			quiet := a.V.GetBool("tools.install.quiet")
			if len(names) == 0 {
				for _, n := range m.Lock.Names() {
					if m.Supported(n) {
						names = append(names, n)
					}
				}
			}
			failed := false
			for _, name := range names {
				if _, ok := m.Lock.Tools[name]; !ok {
					fmt.Fprintf(a.Stderr, "guardrails: unknown tool %q\n", name)
					failed = true
					continue
				}
				if m.Installed(name) {
					if !quiet {
						fmt.Fprintf(a.Stdout, "  ✓ %-14s %s (already installed)\n", name, m.Lock.Tools[name].Version)
					}
					continue
				}
				if err := m.Installer.Install(ctx, name); err != nil {
					fmt.Fprintf(a.Stderr, "  ✗ %-14s %v\n", name, err)
					failed = true
					continue
				}
				if !quiet {
					fmt.Fprintf(a.Stdout, "  ✓ %-14s %s\n", name, m.Lock.Tools[name].Version)
				}
			}
			if failed {
				return exitCode(1)
			}
			return nil
		},
	}
	cmd.Flags().BoolP("quiet", "q", false, "print only failures")
	a.bind(cmd, "tools.install")
	return cmd
}
