package cli

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/rossedman/guardrails/internal/assets"
	"github.com/rossedman/guardrails/internal/config"
)

func newInitCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write .guardrails.yaml and Claude rules into this repo",
		Long: `Set up the repository you're in: write a commented .guardrails.yaml, and
path-scoped rules into .claude/rules/ so Claude knows the standards before
it writes code, not only when the gate blocks it.

Existing files are kept unless --force. Plugins can't ship permission
rules, so init also prints a permissions.deny snippet to merge into
.claude/settings.json for denies that hold even if a hook fails.`,
		Example: `  guardrails init
  guardrails init --rules=false   # only .guardrails.yaml
  guardrails init --force         # overwrite with the latest templates`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, err := a.repo(cmd.Context())
			if err != nil {
				return err
			}
			force := a.V.GetBool("init.force")
			write := func(rel string, data []byte) error {
				p := filepath.Join(repo, rel)
				if _, err := os.Stat(p); err == nil && !force {
					fmt.Fprintf(a.Stdout, "  skip   %s (exists; --force to overwrite)\n", rel)
					return nil
				}
				if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
					return err
				}
				if err := os.WriteFile(p, data, 0o644); err != nil {
					return err
				}
				fmt.Fprintf(a.Stdout, "  write  %s\n", rel)
				return nil
			}
			tmpl, err := fs.ReadFile(assets.FS, "guardrails.yaml")
			if err != nil {
				return err
			}
			if err := write(config.RepoFile, tmpl); err != nil {
				return err
			}
			if a.V.GetBool("init.rules") {
				err := fs.WalkDir(assets.FS, "rules", func(p string, d fs.DirEntry, err error) error {
					if err != nil || d.IsDir() {
						return err
					}
					data, err := fs.ReadFile(assets.FS, p)
					if err != nil {
						return err
					}
					return write(filepath.Join(".claude", p), data)
				})
				if err != nil {
					return err
				}
			}
			snippet, err := fs.ReadFile(assets.FS, "settings.json")
			if err != nil {
				return err
			}
			fmt.Fprintf(a.Stdout, "\nPlugins cannot ship permission rules. For hard denies that hold even if a\nhook fails, merge this into .claude/settings.json:\n\n%s", snippet)
			return nil
		},
	}
	f := cmd.Flags()
	f.Bool("force", false, "overwrite existing files")
	f.Bool("rules", true, "install path-scoped rules into .claude/rules/")
	a.bind(cmd, "init")
	return cmd
}
