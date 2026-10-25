# guardrails config

Print the effective configuration

### Synopsis

Print the configuration guardrails uses in this repository, as YAML: the
defaults, overlaid by the user-wide file (~/.config/guardrails/config.yaml
or --config), overlaid by the repo's .guardrails.yaml.

It fails if either file is invalid, naming every problem.

```
guardrails config [flags]
```

### Examples

```
  guardrails config
  guardrails config --config ./team.yaml
```

### Options

```
  -h, --help   help for config
```

### Options inherited from parent commands

```
      --config string     config file for all your repos (default ~/.config/guardrails/config.yaml)
      --data-dir string   where guardrails keeps state (default $CLAUDE_PLUGIN_DATA, else ~/.local/share/guardrails)
```

### SEE ALSO

* [guardrails](guardrails.md)	 - Quality gates for Claude Code

