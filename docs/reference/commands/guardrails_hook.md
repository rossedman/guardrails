# guardrails hook

Handle a Claude Code hook event (reads JSON on stdin)

### Synopsis

Handle one Claude Code hook event. The plugin's hooks/hooks.json runs this
for every event; you rarely run it yourself.

The event's JSON arrives on stdin and the hook's JSON answer goes to stdout.
Events are SessionStart, UserPromptSubmit, PreToolUse, PostToolUse, Stop,
SubagentStop and SessionEnd, in any case or kebab-case.

If guardrails itself fails on an event that can block (PreToolUse, Stop,
SubagentStop), it exits 2 so the action fails closed. On other events it
exits 1, which Claude Code reports without blocking.

```
guardrails hook <event> [flags]
```

### Examples

```
  # What Claude Code runs when a turn ends
  echo '{"session_id":"s1","cwd":"'$PWD'"}' | guardrails hook Stop
```

### Options

```
  -h, --help   help for hook
```

### Options inherited from parent commands

```
      --config string     config file for all your repos (default ~/.config/guardrails/config.yaml)
      --data-dir string   where guardrails keeps state (default $CLAUDE_PLUGIN_DATA, else ~/.local/share/guardrails)
```

### SEE ALSO

* [guardrails](guardrails.md)	 - Quality gates for Claude Code

