## guardrails doctor

Check that required tools are installed

### Synopsis

Check this repository's config and every tool the language modules run:
where each resolves from (PATH or the managed copy) and its version.

Tools guardrails can provision show as installing on first use; run
"guardrails tools install" to fetch them now. It exits 1 when the config is
invalid or a required tool can't be found or installed.

```
guardrails doctor [flags]
```

### Examples

```
  guardrails doctor
```

### Options

```
  -h, --help   help for doctor
```

### Options inherited from parent commands

```
      --config string     config file for all your repos (default ~/.config/guardrails/config.yaml)
      --data-dir string   where guardrails keeps state (default $CLAUDE_PLUGIN_DATA, else ~/.local/share/guardrails)
```

### SEE ALSO

* [guardrails](guardrails.md)	 - Quality gates for Claude Code

