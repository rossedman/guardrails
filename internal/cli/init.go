package cli

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rossedman/guardrails/internal/assets"
	"github.com/rossedman/guardrails/internal/config"
)

// newInitCmd builds the init command.
func newInitCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write .guardrails.yaml and Claude rules into this repo",
		Long: `Set up the repository you're in: write a short .guardrails.yaml naming a
preset, and path-scoped rules into .claude/rules/ so Claude knows the
standards before it writes code, not only when the gate blocks it.

Presets set the coverage bar; linters, guards and the ratchet are on in all:

  strict    90% coverage, 90% of the lines Claude changes (the default)
  standard  80% coverage, 80% of changed lines
  adopt     for existing repos: coverage may not drop, 80% of changed lines

--full writes every setting with its default instead, to edit by hand.
Existing files are kept unless --force. Plugins can't ship permission
rules, so init also prints a permissions.deny snippet to merge into
.claude/settings.json for denies that hold even if a hook fails.

In Claude Code, /guardrails:setup measures the repo and picks a preset
for you.`,
		Example: `  guardrails init                   # strict
  guardrails init --preset adopt    # an existing repo below the bar
  guardrails init --full            # every setting, spelled out
  guardrails init --statusline      # also print the status line setting`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			preset := a.V.GetString("init.preset")
			if preset == "" || !validPresetName(preset) {
				return usageError{fmt.Errorf("--preset must be one of %s", strings.Join(config.Presets, ", "))}
			}
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
			if err := write(config.RepoFile, configFile(preset, a.V.GetBool("init.full"))); err != nil {
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
			if a.V.GetBool("init.statusline") {
				fmt.Fprintf(a.Stdout, "\nTo show the last gate in Claude Code's status line, merge this into\n~/.claude/settings.json:\n\n%s", statuslineSetting(a.launcher()))
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.String("preset", config.PresetStrict, "coverage bar: "+strings.Join(config.Presets, ", "))
	f.Bool("full", false, "write every setting with its default, not just the preset")
	f.Bool("force", false, "overwrite existing files")
	f.Bool("rules", true, "install path-scoped rules into .claude/rules/")
	f.Bool("statusline", false, "also print the statusLine setting for ~/.claude/settings.json")
	a.bind(cmd, "init")
	return cmd
}

// validPresetName reports whether name is one of the config presets.
func validPresetName(name string) bool { return slices.Contains(config.Presets, name) }

const schemaLine = "# yaml-language-server: $schema=https://raw.githubusercontent.com/rossedman/guardrails/main/internal/config/schema.json\n"

// configFile renders .guardrails.yaml for a preset: just the preset, or with
// --full every setting at the preset's values.
// The preset has been validated.
func configFile(preset string, full bool) []byte {
	if !full {
		return []byte(schemaLine + `#
# Presets: strict (90% coverage, 90% of changed lines), standard (80%/80%),
# adopt (coverage may not drop, 80% of changed lines). Any key set here
# overrides the preset. Docs: https://rossedman.github.io/guardrails/configuration/
version: 1
preset: ` + preset + "\n")
	}
	tmpl, _ := fs.ReadFile(assets.FS, "guardrails.yaml") // embedded
	cfg := config.Default()
	_ = config.ApplyPreset(&cfg, preset)
	num := func(v float64) string { return fmt.Sprintf("%g", v) }
	r := strings.NewReplacer(
		"preset: strict ", "preset: "+preset+strings.Repeat(" ", max(1, len("strict ")-len(preset))),
		"total: 90", "total: "+num(cfg.Go.Coverage.Total),
		"per_package_floor: 80", "per_package_floor: "+num(cfg.Go.Coverage.PerPackageFloor),
		"patch_coverage: 90", "patch_coverage: "+num(cfg.Go.PatchCoverage),
	)
	return []byte(r.Replace(string(tmpl)))
}

// statuslineSetting is the statusLine block for ~/.claude/settings.json,
// with the absolute path of the command so it works outside the plugin.
func statuslineSetting(command string) string {
	b, _ := json.MarshalIndent(map[string]any{
		"statusLine": map[string]any{"type": "command", "command": command + " statusline", "refreshInterval": 10},
	}, "", "  ")
	return string(b) + "\n"
}
