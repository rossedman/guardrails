package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/rossedman/guardrails/internal/engine"
	"github.com/rossedman/guardrails/internal/hook"
)

var hookEvents = []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "Stop", "SubagentStop", "SessionEnd"}

// newHookCmd builds the hook command, which handles one Claude Code hook event.
func newHookCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "hook <event>",
		Short: "Handle a Claude Code hook event (reads JSON on stdin)",
		Long: `Handle one Claude Code hook event. The plugin's hooks/hooks.json runs this
for every event; you rarely run it yourself.

The event's JSON arrives on stdin and the hook's JSON answer goes to stdout.
Events are SessionStart, UserPromptSubmit, PreToolUse, PostToolUse, Stop,
SubagentStop and SessionEnd, in any case or kebab-case.

If guardrails itself fails on an event that can block (PreToolUse, Stop,
SubagentStop), it exits 2 so the action fails closed. On other events it
exits 1, which Claude Code reports without blocking.`,
		Example: `  # What Claude Code runs when a turn ends
  echo '{"session_id":"s1","cwd":"'$PWD'"}' | guardrails hook Stop`,
		Args: usageArgs(func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("hook takes one event: %v", hookEvents)
			}
			if _, ok := engine.NormalizeEvent(args[0]); !ok {
				return fmt.Errorf("unknown hook event %q", args[0])
			}
			return nil
		}),
		ValidArgs: hookEvents,
		RunE: func(cmd *cobra.Command, args []string) error {
			event, _ := engine.NormalizeEvent(args[0])
			fail := func(err error) error {
				fmt.Fprintf(a.Stderr, "guardrails %s: %v\n", event, err)
				if engine.Blocking(event) {
					return exitCode(2)
				}
				return exitCode(1)
			}
			in, err := hook.ReadInput(a.Stdin)
			if err != nil {
				return fail(err)
			}
			out, err := a.engine().Handle(cmd.Context(), event, in)
			if err != nil {
				return fail(err)
			}
			if err := out.Write(a.Stdout); err != nil {
				return fail(err)
			}
			return nil
		},
	}
}
