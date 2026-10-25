## guardrails init

Write .guardrails.yaml and Claude rules into this repo

### Synopsis

Set up the repository you're in: write a short .guardrails.yaml naming a
preset, and path-scoped rules into .claude/rules/ so Claude knows the
standards before it writes code, not only when the gate blocks it.

Presets set the coverage bar; linters, guards and the ratchet are on in all:

  strict    90% coverage, 90% of the lines Claude changes (the default)
  standard  80% coverage, 80% of changed lines
  adopt     for existing repos: coverage may not drop, 80% of changed lines

--full writes every setting with its default instead, to edit by hand.
Existing files are kept unless --force. Plugins can't ship permission
rules, so init also prints a permissions.deny snippet to merge into
.claude/settings.json for denies that hold even if a hook fails.

In Claude Code, /guardrails:setup measures the repo and picks a preset
for you.

```
guardrails init [flags]
```

### Examples

```
  guardrails init                   # strict
  guardrails init --preset adopt    # an existing repo below the bar
  guardrails init --full            # every setting, spelled out
  guardrails init --statusline      # also print the status line setting
```

### Options

```
      --force           overwrite existing files
      --full            write every setting with its default, not just the preset
  -h, --help            help for init
      --preset string   coverage bar: strict, standard, adopt (default "strict")
      --rules           install path-scoped rules into .claude/rules/ (default true)
      --statusline      also print the statusLine setting for ~/.claude/settings.json
```

### Options inherited from parent commands

```
      --config string     config file for all your repos (default ~/.config/guardrails/config.yaml)
      --data-dir string   where guardrails keeps state (default $CLAUDE_PLUGIN_DATA, else ~/.local/share/guardrails)
```

### SEE ALSO

* [guardrails](guardrails.md)	 - Quality gates for Claude Code

