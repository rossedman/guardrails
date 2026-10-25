## guardrails tools list

Show every tool, its pinned version and where it resolves from

### Synopsis

List every tool guardrails knows, its pinned version and source (a release
archive, PyPI, uv or go install), and what it resolves to now: a binary on
your PATH, the managed copy, or not installed yet.

```
guardrails tools list [flags]
```

### Examples

```
  guardrails tools list
```

### Options

```
  -h, --help   help for list
```

### Options inherited from parent commands

```
      --config string     config file for all your repos (default ~/.config/guardrails/config.yaml)
      --data-dir string   where guardrails keeps state (default $CLAUDE_PLUGIN_DATA, else ~/.local/share/guardrails)
```

### SEE ALSO

* [guardrails tools](guardrails_tools.md)	 - List, install or locate the external tools Guardrails runs

