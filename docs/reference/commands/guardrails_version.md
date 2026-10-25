## guardrails version

Print version information

### Synopsis

Print the guardrails version, the commit it was built from and when.

Release builds carry their tag; local builds report "dev". The plugin's
launcher downloads the release matching .claude-plugin/plugin.json.

```
guardrails version [flags]
```

### Examples

```
  guardrails version
  guardrails --version
```

### Options

```
  -h, --help   help for version
```

### Options inherited from parent commands

```
      --config string     config file for all your repos (default ~/.config/guardrails/config.yaml)
      --data-dir string   where guardrails keeps state (default $CLAUDE_PLUGIN_DATA, else ~/.local/share/guardrails)
```

### SEE ALSO

* [guardrails](guardrails.md)	 - Quality gates for Claude Code

