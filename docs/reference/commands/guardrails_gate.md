# guardrails gate

Run the full quality gate on changed files (or --all)

### Synopsis

Run the gate Claude meets when it stops: every enabled language module
builds, tests, measures coverage and lints the files changed against --base
(plus untracked files), or every package with --all.

It exits 1 when a check fails and prints the same short report Claude sees;
--explain prints all of it.

--staged gates only files staged for commit, for pre-commit hooks.

```
guardrails gate [flags]
```

### Examples

```
  guardrails gate
  guardrails gate --base main     # everything this branch changed
  guardrails gate --all --json    # every package, for CI
  guardrails gate --staged        # in a pre-commit hook
```

### Options

```
      --all           gate every package, not just changed files
      --base string   git ref that changes and patch coverage are measured against (default "HEAD")
      --explain       print the full, untruncated report
  -h, --help          help for gate
      --json          print checks as JSON
      --staged        gate only files staged for commit; patch coverage is measured against HEAD
```

### Options inherited from parent commands

```
      --config string     config file for all your repos (default ~/.config/guardrails/config.yaml)
      --data-dir string   where guardrails keeps state (default $CLAUDE_PLUGIN_DATA, else ~/.local/share/guardrails)
```

### SEE ALSO

* [guardrails](guardrails.md)	 - Quality gates for Claude Code

