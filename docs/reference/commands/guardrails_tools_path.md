# guardrails tools path

Print the command used to run a tool, installing it if needed

### Synopsis

Print the command guardrails runs for a tool, resolving it the way hooks
do and installing the pinned version if it's missing. Tools run through an
interpreter print the interpreter and its arguments too.

```
guardrails tools path <tool> [flags]
```

### Examples

```
  guardrails tools path ruff
  $(guardrails tools path shellcheck) scripts/*.sh
```

### Options

```
  -h, --help   help for path
```

### Options inherited from parent commands

```
      --config string     config file for all your repos (default ~/.config/guardrails/config.yaml)
      --data-dir string   where guardrails keeps state (default $CLAUDE_PLUGIN_DATA, else ~/.local/share/guardrails)
```

### SEE ALSO

* [guardrails tools](guardrails_tools.md)	 - List, install or locate the external tools Guardrails runs

