# Learn

The gate catches problems; learning **prevents** them. Guardrails keeps two
local records of what Claude gets wrong:

- **Gate history.** Every Stop gate run is logged, so you can see which
  checks Claude trips most, turn after turn.
- **Your corrections.** You tell Claude "don't mock the database here". It
  apologizes, fixes it, and next session does it again: the correction lived
  only in that conversation. Guardrails records corrections like that as
  **lessons**.

`/guardrails:learn` turns both into rules in `.claude/rules/`, which Claude
reads before it writes code, so the next session meets the bar the first
time.

```bash
guardrails learn                  # this repo, last 30 days
guardrails learn --days 90        # a longer window
guardrails learn --all-repos      # every repo on this machine
guardrails learn --json           # for scripts and the skill
```

```text
Gate history — last 30 days, this repo

  first-pass rate   62% (31 of 50 turns passed the gate first time)
  blocks per turn   0.6
  hand-offs         2
  sessions          41 (82 gate runs)
  gate time         p50 34.0s, p95 71.2s

Top rules tripped
  RULE                                 SESSIONS  TIMES  EXAMPLE
  lint/errcheck                              14     23  api/handler.go
  coverage                                   11     11
  lint/SC2086                                 5      9  scripts/deploy.sh

Rule candidates (tripped in many sessions; teach them up front)
  lint/errcheck                        14 sessions → .claude/rules/go.md
  coverage                             11 sessions → .claude/rules/go.md
  lint/SC2086                          5 sessions → .claude/rules/shell.md

Your corrections — 3 recorded, this repo

  2026-10-16  7c21d04e9a * From now on, run make lint before you say you're done.
  2026-10-14  3f9a1c0b2e   Don't mock the database in the store tests.
  2026-10-12  b80e5a1f33   We don't use testify in this codebase.

* you asked for it to be remembered
Run /guardrails:learn in Claude Code to turn these into rules.
```

## Turning them into rules

Run `/guardrails:learn` in Claude Code. The skill:

1. reads `guardrails learn --json`: the gate history (`insights`) and your
   corrections (`lessons`);
2. explains the trend: the first-pass rate and the rules tripped most;
3. groups corrections that say the same thing and sets aside one-offs
   ("revert that") that aren't rules;
4. drafts a concrete rule for each rule candidate and each convention, in
   the right `.claude/rules/` file for its language or path, or `CLAUDE.md`
   for general workflow, citing its evidence ("errcheck flagged this in 14
   sessions", "you corrected this on 2026-10-14");
5. shows the additions as a diff for you to apply — `.claude/rules` files
   are human-owned, and Guardrails denies Claude's edits to them;
6. clears the corrections it handled.

When corrections are waiting, Guardrails tells you at the start of the next
session, and `/guardrails:status` suggests `/guardrails:learn` as the next
step.

## Gate history

| Metric | Meaning |
|---|---|
| **First-pass rate** | Share of turns that passed the Stop gate on the first try. This is the success metric: it should trend up as the rules teach Claude your standards. |
| Blocks per turn | Average number of times the gate blocked before a turn ended. |
| Hand-offs | Turns where the gate gave up after `gate.max_consecutive_blocks` and handed the turn back to you. |
| Timeouts | Gate runs that exceeded `gate.timeout_seconds`. |
| Gate time | Median (p50) and p95 duration of gate runs. |
| Top rules | The checks and linter rules that failed most, by the number of distinct sessions they appeared in, with example files. |

A *turn* is the sequence of gate runs in one session (per subagent) that
ends in a pass or a hand-off.

A rule that trips in at least `--min-sessions` sessions (default 5) is a
**rule candidate**: Claude keeps making the same mistake, so the fix
belongs in a rules file that loads before it writes code, not in another
gate failure. Each candidate is mapped to the rules file for its language
(`go.md`, `python.md`, `shell.md`, `yaml.md`, `terraform.md`, `docker.md`).

## Your corrections

Every prompt you send passes through Guardrails' `UserPromptSubmit` hook.
If it reads like a correction or a standing instruction, Guardrails records
a lesson: the time, session, repository, your prompt (up to 2,000
characters) and which cues matched.

Detection is deliberately conservative, because a false positive would end
up as noise in your rules:

| Recorded | Not recorded |
|---|---|
| "No, use the existing helper." | "No, that's fine, ship it." |
| "Don't mock the database." | "I don't know why the build is slow." |
| "We don't use testify here." | "Why does this test fail?" |
| "Stop adding comments to every line." | "Should I use `errors.Is` instead?" |
| "That's not what I asked for." | Slash commands such as `/guardrails:gate` |
| "Use `errors.Is` instead of comparing strings." | Text in code blocks, backticks or quotes |
| "Why did you delete the migration?" | Pasted logs and long specs (over 4,000 characters, or mostly code) |

Prompts with "from now on", "going forward", "always", "never", "remember
to", "in this repo we" or "our convention is" are marked **explicit**
(`*`): you asked for them to be remembered, so they're the strongest
candidates.

When a correction is recorded, Claude is told so: it should follow the
correction for the rest of the session, and you can run `/guardrails:learn`
to keep it. Turn this off with `lessons.nudge: false`; lessons are still
recorded.

To remove corrections without turning them into rules:

```bash
guardrails learn --clear --id 3f9a1c0b2e  # remove some
guardrails learn --clear                  # remove all of this repo's
```

## What's recorded, and where

Both records live in the plugin data directory (`${CLAUDE_PLUGIN_DATA}`,
or `~/.local/share/guardrails`):

- `runs.jsonl`, gate runs: time, session, repository path, whether it
  passed, the attempt number, whether it handed off, the language modules
  that ran, coverage, duration, and each failure's check, rule and file
  path; and sessions: when they ended, how many gates ran, how many blocked,
  and how many files were touched. No source code, test output or prompts.
- `lessons.jsonl`, corrections, as above.

**Nothing leaves your machine**: `learn` reads the local files and nothing
else. Delete either file at any time to start over.

## Configuration

```yaml
lessons:
  enabled: true   # record corrections
  nudge: true     # tell Claude when one is recorded
```
