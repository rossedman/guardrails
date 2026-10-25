package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/rossedman/guardrails/internal/config"
	"github.com/rossedman/guardrails/internal/tools"
)

// newDoctorCmd builds the doctor command, which checks config, runtimes and tools.
func newDoctorCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check that required tools are installed",
		Long: `Check this repository's config and every tool the language modules run:
where each resolves from (PATH or the managed copy) and its version.

Tools guardrails can provision show as installing on first use; run
"guardrails tools install" to fetch them now. It exits 1 when the config is
invalid or a required tool can't be found or installed.`,
		Example: `  guardrails doctor`,
		Args:    usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			e := a.engine()
			missingRequired := false
			fmt.Fprintf(a.Stdout, "guardrails %s\n", Version)
			fmt.Fprintf(a.Stdout, "data dir: %s\n", a.DataDir())
			cfg := config.Default()
			if repo, err := a.repo(ctx); err == nil {
				if loaded, err := config.Load(a.globalConfig(), repo); err != nil {
					fmt.Fprintf(a.Stdout, "config:   ✗ %v\n", err)
					missingRequired = true
				} else {
					cfg = loaded
					fmt.Fprintf(a.Stdout, "config:   ✓ %s\n", repo)
				}
			}
			mgr := a.toolManager()
			if mgr != nil {
				mgr.Prefer = cfg.Tools.Prefer
				mgr.AutoInstall = cfg.Tools.Install != config.InstallNever
			}
			for _, m := range e.Modules {
				fmt.Fprintf(a.Stdout, "\n%s module\n", m.Name())
				for _, t := range m.Tools() {
					kind := "optional"
					if t.Required {
						kind = "required"
					}
					path, where := a.locateTool(mgr, t.Name)
					switch {
					case path != "":
						version := ""
						if len(t.VersionArgs) > 0 {
							if res, err := a.Runner.Run(ctx, "", path, t.VersionArgs...); err == nil {
								if ls := trimLines(res.Combined()); len(ls) > 0 {
									version = ls[0]
								}
							}
						}
						fmt.Fprintf(a.Stdout, "  ✓ %-14s %s (%s) %s\n", t.Name, path, where, version)
					case mgr != nil && mgr.AutoInstall && mgr.Supported(t.Name):
						fmt.Fprintf(a.Stdout, "  ○ %-14s %s, installs v%s on first use (guardrails tools install)\n", t.Name, kind, mgr.Lock.Tools[t.Name].Version)
					default:
						if t.Required {
							missingRequired = true
						}
						fmt.Fprintf(a.Stdout, "  ✗ %-14s %s, not found — %s\n", t.Name, kind, t.Install)
					}
				}
			}
			if missingRequired {
				return exitCode(1)
			}
			return nil
		},
	}
}

// locateTool finds a tool the way hooks resolve it, without installing:
// PATH first (unless the managed copy is preferred), then the managed copy.
func (a *App) locateTool(mgr *tools.Manager, name string) (path, where string) {
	onPath := func() string {
		p, err := a.Runner.LookPath(name)
		if err != nil {
			return ""
		}
		return p
	}
	if mgr == nil || mgr.Prefer != tools.PreferManaged {
		if p := onPath(); p != "" {
			return p, "PATH"
		}
	}
	if mgr != nil && mgr.Installed(name) {
		return mgr.ManagedPath(name), "managed"
	}
	if p := onPath(); p != "" {
		return p, "PATH"
	}
	return "", ""
}
