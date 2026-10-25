## guardrails

Quality gates for Claude Code

### Synopsis

guardrails makes Claude Code meet your quality bar before it says "done".

As a Claude Code plugin it formats and lints every file Claude edits, blocks
risky commands and edits before they run, and gates every turn: when Claude
stops, the changed code must build, pass its tests and meet coverage, or
Claude gets a short report and keeps working.

The same gate runs from your terminal, in CI and as a pre-commit hook.

```
guardrails [flags]
```

### Examples

```
  # What is guardrails doing in this repo?
  guardrails status

  # Set a repo up (an existing repo below the bar: --preset adopt)
  guardrails init

  # Run the gate Claude faces, on your changes or on everything
  guardrails gate
  guardrails gate --all

  # What Claude keeps getting wrong, from the gate and your corrections
  guardrails learn
```

### Options

```
      --config string     config file for all your repos (default ~/.config/guardrails/config.yaml)
      --data-dir string   where guardrails keeps state (default $CLAUDE_PLUGIN_DATA, else ~/.local/share/guardrails)
  -h, --help              help for guardrails
```

### SEE ALSO

* [guardrails config](guardrails_config.md)	 - Print the effective configuration
* [guardrails doctor](guardrails_doctor.md)	 - Check that required tools are installed
* [guardrails gate](guardrails_gate.md)	 - Run the full quality gate on changed files (or --all)
* [guardrails harden](guardrails_harden.md)	 - Run mutation tests and fuzzing on the Go code you changed (on demand)
* [guardrails hook](guardrails_hook.md)	 - Handle a Claude Code hook event (reads JSON on stdin)
* [guardrails init](guardrails_init.md)	 - Write .guardrails.yaml and Claude rules into this repo
* [guardrails learn](guardrails_learn.md)	 - What Claude keeps getting wrong, from the gate and from your corrections
* [guardrails schema](guardrails_schema.md)	 - Print the config JSON Schema
* [guardrails status](guardrails_status.md)	 - What guardrails is doing in this repo, and what to do next
* [guardrails statusline](guardrails_statusline.md)	 - Print a one-line status for Claude Code's status line
* [guardrails tools](guardrails_tools.md)	 - List, install or locate the external tools Guardrails runs
* [guardrails version](guardrails_version.md)	 - Print version information

