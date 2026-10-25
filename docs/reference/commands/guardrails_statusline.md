## guardrails statusline

Print a one-line status for Claude Code's status line

### Synopsis

Print one line with the last gate's result, coverage against its threshold,
and lint issue count, for Claude Code's statusLine setting. It reads Claude
Code's status JSON on stdin to find the workspace.

It never fails: a status line must not break the UI.

```
guardrails statusline [flags]
```

### Examples

```
  # In ~/.claude/settings.json
  "statusLine": {"type": "command", "command": "guardrails statusline"}
```

### Options

```
  -h, --help   help for statusline
```

### Options inherited from parent commands

```
      --config string     config file for all your repos (default ~/.config/guardrails/config.yaml)
      --data-dir string   where guardrails keeps state (default $CLAUDE_PLUGIN_DATA, else ~/.local/share/guardrails)
```

### SEE ALSO

* [guardrails](guardrails.md)	 - Quality gates for Claude Code

