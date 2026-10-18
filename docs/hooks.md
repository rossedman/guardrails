# Hook events

Seven events do the work. Everything that blocks uses a JSON `decision` (or
`permissionDecision`) with exit code 0, never exit 1, which Claude Code
treats as a non-blocking error.

| Event | Matcher | What runs | Effect on failure | Timeout |
|---|---|---|---|---|
| SessionStart | `startup\|resume\|clear\|compact` | Detect toolchains, verify tools, inject the repo's standards and baseline as `additionalContext` | Context only; missing tools go to `systemMessage` | 10s |
| UserPromptSubmit | — | Record prompts that correct Claude as [lessons](lessons.md) | Context only; never blocks a prompt | 3s |
| PreToolUse | `Edit\|Write\|MultiEdit\|NotebookEdit\|Bash` | [Policy guards](guards.md) and the [dependency check](dependencies.md) | `permissionDecision: deny` or `ask`, with a reason | 10s |
| PostToolUse | `Edit\|Write\|MultiEdit` | Auto-format the file, then fast lint on its package; record it as touched | `decision: block` with the report (the edit already happened) | 5s |
| Stop | — | Full gate over touched packages | `decision: block`; after 3 consecutive blocks, hands off to you | 120s |
| SubagentStop | `test-writer\|implementer` | Same gate, scoped to the files that subagent touched | Same as Stop | 120s |
| SessionEnd | `*` | Append a session record to `runs.jsonl` | None | 3s |

SessionStart's timeout is generous because the first call may download the
binary. PreToolUse allows 10s because the dependency check looks packages up
on deps.dev and OSV; it only does so for install commands and manifest
edits, and caches what it finds for a day.

## The plugin's `hooks.json`

Hooks use exec form (`args`), so a plugin path containing spaces never
breaks, and blocking hooks fail closed:

```json
{
  "type": "command",
  "command": "${CLAUDE_PLUGIN_ROOT}/bin/guardrails",
  "args": ["hook", "Stop"],
  "timeout": 120,
  "onFailure": "block"
}
```

## Session lifecycle

- **SessionStart** with `startup` or `clear` starts a fresh gate: it records
  `HEAD` as the base commit for patch coverage, snapshots the per-package
  coverage baseline from the repo cache, and clears touched files and retry
  counters. `resume` and `compact` keep the session's history.
- **PostToolUse** adds the edited file to the touched set (and to the
  subagent's own set when a subagent made the edit).
- **Stop** passes immediately when nothing was touched. Otherwise it runs
  the gate. A pass resets the retry counter and raises the ratchet baseline
  to the new coverage. A failure blocks with `attempt N of 3`; the fourth
  consecutive failure stops blocking, puts the summary in `systemMessage`
  for you, and lets the turn end.

## Warn mode

With `mode: warn`, PostToolUse and Stop report failures in `systemMessage`
instead of blocking. Policy guards are unaffected: they always apply.

## Config errors

If `.guardrails.yaml` fails to load (an unknown key, a bad value), quality
checks pause and every event reports the error to you in `systemMessage`.
Claude is told the config is the human's to fix. Policy guards keep running
with the built-in defaults, so a broken config never disables protection of
the config itself.
