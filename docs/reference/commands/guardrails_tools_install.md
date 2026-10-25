## guardrails tools install

Install pinned tools (all supported tools when none are named)

### Synopsis

Download, verify and install the pinned version of each named tool into the
data dir, or of every tool supported on this platform when none are named.
Tools already installed are skipped.

Hooks install tools in the background on first use; run this to do it up
front, as the Stop hook and CI do.

```
guardrails tools install [tool...] [flags]
```

### Examples

```
  guardrails tools install
  guardrails tools install --quiet golangci-lint gofumpt
```

### Options

```
  -h, --help    help for install
  -q, --quiet   print only failures
```

### Options inherited from parent commands

```
      --config string     config file for all your repos (default ~/.config/guardrails/config.yaml)
      --data-dir string   where guardrails keeps state (default $CLAUDE_PLUGIN_DATA, else ~/.local/share/guardrails)
```

### SEE ALSO

* [guardrails tools](guardrails_tools.md)	 - List, install or locate the external tools Guardrails runs

