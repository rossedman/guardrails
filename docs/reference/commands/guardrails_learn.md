# guardrails learn

What Claude keeps getting wrong, from the gate and from your corrections

### Synopsis

Show what's worth teaching Claude up front, from two sources:

  the gate     how often Claude passes first time, the rules it trips most,
               and rule candidates: rules tripped in --min-sessions sessions
  you          the corrections you gave Claude ("don't mock the database")
               and standing rules ("from now on, run make lint"), recorded
               when you sent them

Everything stays on this machine. In Claude Code, /guardrails:learn turns
this into .claude/rules additions for you to review; clear the corrections
you've handled with --clear.

```
guardrails learn [flags]
```

### Examples

```
  guardrails learn
  guardrails learn --days 7 --all-repos
  guardrails learn --json
  guardrails learn --clear --id 3f9a1c0b2e   # a handled correction
  guardrails learn --clear                   # all of this repo's
```

### Options

```
      --all-repos          include every repository, not just this one
      --clear              remove this repo's recorded corrections (every repo's with --all-repos)
      --days int           gate history window in days (default 30)
  -h, --help               help for learn
      --id strings         with --clear, remove only these corrections
      --json               print {"insights": ..., "lessons": [...]} as JSON
      --min-sessions int   sessions a rule must trip in to become a rule candidate (default 5)
```

### Options inherited from parent commands

```
      --config string     config file for all your repos (default ~/.config/guardrails/config.yaml)
      --data-dir string   where guardrails keeps state (default $CLAUDE_PLUGIN_DATA, else ~/.local/share/guardrails)
```

### SEE ALSO

* [guardrails](guardrails.md)	 - Quality gates for Claude Code

