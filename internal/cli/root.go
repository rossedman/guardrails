package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// NewRootCmd builds the command tree.
func NewRootCmd(a *App) *cobra.Command {
	root := &cobra.Command{
		Use:   "guardrails",
		Short: "Quality gates for Claude Code",
		Long: `guardrails makes Claude Code meet your quality bar before it says "done".

As a Claude Code plugin it formats and lints every file Claude edits, blocks
risky commands and edits before they run, and gates every turn: when Claude
stops, the changed code must build, pass its tests and meet coverage, or
Claude gets a short report and keeps working.

The same gate runs from your terminal, in CI and as a pre-commit hook.`,
		Example: `  # What is guardrails doing in this repo?
  guardrails status

  # Set a repo up (an existing repo below the bar: --preset adopt)
  guardrails init

  # Run the gate Claude faces, on your changes or on everything
  guardrails gate
  guardrails gate --all

  # What Claude keeps getting wrong, from the gate and your corrections
  guardrails learn`,
		Version:       fmt.Sprintf("%s (commit %s, built %s)", Version, Commit, Date),
		SilenceUsage:  true,
		SilenceErrors: true,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return nil
			}
			msg := fmt.Sprintf("unknown command %q for %q", args[0], cmd.CommandPath())
			if s := cmd.SuggestionsFor(args[0]); len(s) > 0 {
				msg += "\n\nDid you mean this?\n\t" + strings.Join(s, "\n\t")
			}
			return usageError{errors.New(msg)}
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.helpAndFail(cmd)
		},
	}
	root.SetVersionTemplate("guardrails {{.Version}}\n")
	root.SetIn(a.Stdin)
	root.SetOut(a.Stdout)
	root.SetErr(a.Stderr)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageError{err} })
	a.setupHelp(root)

	pf := root.PersistentFlags()
	pf.String("data-dir", "", "where guardrails keeps state (default $CLAUDE_PLUGIN_DATA, else ~/.local/share/guardrails)")
	pf.String("config", "", "user-wide config file (default ~/.config/guardrails/config.yaml)")
	_ = a.V.BindPFlag(keyDataDir, pf.Lookup("data-dir"))
	_ = a.V.BindPFlag(keyConfig, pf.Lookup("config"))

	root.AddGroup(
		&cobra.Group{ID: "daily", Title: "Daily:"},
		&cobra.Group{ID: "setup", Title: "Setup:"},
		&cobra.Group{ID: "claude", Title: "Claude Code:"},
	)
	add := func(group string, cmds ...*cobra.Command) {
		for _, c := range cmds {
			c.GroupID = group
			root.AddCommand(c)
		}
	}
	add("daily", newStatusCmd(a), newGateCmd(a), newHardenCmd(a), newLearnCmd(a))
	add("setup", newInitCmd(a), newDoctorCmd(a), newToolsCmd(a), newConfigCmd(a), newSchemaCmd(a))
	add("claude", newHookCmd(a), newStatuslineCmd(a))
	// Older names, kept working but out of the way: gate --explain and learn
	// cover them.
	add("daily", newExplainCmd(a), newInsightsCmd(a), newLessonsCmd(a))
	root.AddCommand(newVersionCmd(a))
	return root
}

// Run executes args (without the program name) and returns the exit code:
// 0 on success, 1 when a command fails, 2 for usage errors.
func (a *App) Run(ctx context.Context, args []string) int {
	a.V = newViper()
	root := NewRootCmd(a)
	root.SetArgs(args)
	cmd, err := root.ExecuteContextC(ctx)
	if err == nil {
		return 0
	}
	var code exitCode
	if errors.As(err, &code) {
		return int(code)
	}
	fmt.Fprintf(a.Stderr, "guardrails: %v\n", err)
	var usage usageError
	if errors.As(err, &usage) {
		fmt.Fprintf(a.Stderr, "Run '%s --help' for usage.\n", cmd.CommandPath())
		return 2
	}
	return 1
}

// exitCode ends a command with a status after it printed its own output,
// like a gate that failed.
type exitCode int

func (c exitCode) Error() string { return fmt.Sprintf("exit status %d", int(c)) }

// usageError is a mistake in how a command was called; it exits 2.
type usageError struct{ err error }

func (e usageError) Error() string { return e.err.Error() }
func (e usageError) Unwrap() error { return e.err }

// usageArgs makes an argument check's failure a usage error.
func usageArgs(check cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := check(cmd, args); err != nil {
			return usageError{err}
		}
		return nil
	}
}

// helpAndFail prints help to stderr for a command that needs a subcommand
// it wasn't given, and exits 2.
func (a *App) helpAndFail(cmd *cobra.Command) error {
	_ = a.renderHelp(a.Stderr, cmd)
	return exitCode(2)
}

// bind makes every local flag of cmd readable from Viper as prefix.<flag>,
// and so settable as GUARDRAILS_<PREFIX>_<FLAG>.
func (a *App) bind(cmd *cobra.Command, prefix string) {
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		_ = a.V.BindPFlag(prefix+"."+f.Name, f)
	})
}
