# guardrails status

What guardrails is doing in this repo, and what to do next

### Synopsis

Show what guardrails does in the repository you're in: the preset and
config files that apply, the languages it gates and the bar for each, how
the last gate went, corrections waiting to become rules, and the one thing
worth doing next.

In Claude Code, /guardrails:status shows the same.

```
guardrails status [flags]
```

### Examples

```
  guardrails status
  guardrails status --json
```

### Options

```
  -h, --help   help for status
      --json   print the status as JSON
```

### Options inherited from parent commands

```
      --config string     config file for all your repos (default ~/.config/guardrails/config.yaml)
      --data-dir string   where guardrails keeps state (default $CLAUDE_PLUGIN_DATA, else ~/.local/share/guardrails)
```

### SEE ALSO

* [guardrails](guardrails.md)	 - Quality gates for Claude Code

