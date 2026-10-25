# guardrails tools

List, install or locate the external tools Guardrails runs

### Synopsis

Manage the linters, formatters and scanners the language modules run.

Each tool is pinned in tools.yaml and checksum-locked per platform. A tool
on your PATH is used first (unless tools.prefer is managed); otherwise
guardrails installs the pinned version into its data dir on first use, so
the host needs nothing but the plugin.

```
guardrails tools [flags]
```

### Examples

```
  guardrails tools list
  guardrails tools install            # everything, e.g. in CI or a Dockerfile
  guardrails tools install ruff shellcheck
  guardrails tools path golangci-lint
```

### Options

```
  -h, --help   help for tools
```

### Options inherited from parent commands

```
      --config string     config file for all your repos (default ~/.config/guardrails/config.yaml)
      --data-dir string   where guardrails keeps state (default $CLAUDE_PLUGIN_DATA, else ~/.local/share/guardrails)
```

### SEE ALSO

* [guardrails](guardrails.md)	 - Quality gates for Claude Code
* [guardrails tools install](guardrails_tools_install.md)	 - Install pinned tools (all supported tools when none are named)
* [guardrails tools list](guardrails_tools_list.md)	 - Show every tool, its pinned version and where it resolves from
* [guardrails tools path](guardrails_tools_path.md)	 - Print the command used to run a tool, installing it if needed

