## guardrails schema

Print the config JSON Schema

### Synopsis

Print the JSON Schema for .guardrails.yaml and the user-wide config, for
editor completion and validation.

```
guardrails schema [flags]
```

### Examples

```
  guardrails schema > .guardrails.schema.json
```

### Options

```
  -h, --help   help for schema
```

### Options inherited from parent commands

```
      --config string     config file for all your repos (default ~/.config/guardrails/config.yaml)
      --data-dir string   where guardrails keeps state (default $CLAUDE_PLUGIN_DATA, else ~/.local/share/guardrails)
```

### SEE ALSO

* [guardrails](guardrails.md)	 - Quality gates for Claude Code

