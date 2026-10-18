# Lessons

You tell Claude "don't mock the database here". It apologizes, fixes it, and
next session does it again: the correction lived only in that
conversation. Guardrails records corrections like that as **lessons**, and
`/guardrails:learn` turns them into rules Claude reads before it writes
code.

```text
guardrails lessons — 3 recorded, this repo

  2026-10-16  7c21d04e9a * From now on, run make lint before you say you're done.
  2026-10-14  3f9a1c0b2e   Don't mock the database in the store tests.
  2026-10-12  b80e5a1f33   We don't use testify in this codebase.

* you asked for it to be remembered
Run /guardrails:learn in Claude Code to turn these into rules.
```

## How capture works

Every prompt you send passes through Guardrails' `UserPromptSubmit` hook.
If it reads like a correction or a standing instruction, Guardrails appends
a lesson to `lessons.jsonl` in its data directory: the time, session,
repository, your prompt (up to 2,000 characters) and which cues matched.
Nothing leaves your machine, and nothing is sent anywhere.

Detection is deliberately conservative, because a false positive would end
up as noise in your rules:

| Recorded | Not recorded |
|---|---|
| "No, use the existing helper." | "No, that's fine, ship it." |
| "Don't mock the database." | "I don't know why the build is slow." |
| "We don't use testify here." | "Why does this test fail?" |
| "Stop adding comments to every line." | "Should I use `errors.Is` instead?" |
| "That's not what I asked for." | Slash commands such as `/guardrails:check` |
| "Use `errors.Is` instead of comparing strings." | Text in code blocks, backticks or quotes |
| "Why did you delete the migration?" | Pasted logs and long specs (over 4,000 characters, or mostly code) |

Prompts with "from now on", "going forward", "always", "never", "remember
to", "in this repo we" or "our convention is" are marked **explicit**
(`*`): you asked for them to be remembered, so they're the strongest
candidates.

## The nudge

When a correction is recorded, Claude is told so: it should follow the
correction for the rest of the session, and you can run
`/guardrails:learn` to keep it. Turn this off with `lessons.nudge: false`;
lessons are still recorded.

## Turning lessons into rules

Run `/guardrails:learn` in Claude Code. The skill:

1. reads `guardrails lessons --json`;
2. groups lessons that say the same thing and sets aside one-offs ("revert
   that") that aren't rules;
3. drafts a concrete rule for each convention, in the right
   `.claude/rules/` file for its language or path, or `CLAUDE.md` for
   general workflow, citing when you made the correction;
4. shows the additions as a diff for you to apply — `.claude/rules` files
   are human-owned, and Guardrails denies Claude's edits to them;
5. clears the lessons it handled.

## The CLI

```bash
guardrails lessons                          # this repo, newest first
guardrails lessons --all-repos              # every repo on this machine
guardrails lessons --json                   # for scripts and the skill
guardrails lessons --clear --id 3f9a1c0b2e  # remove handled lessons
guardrails lessons --clear                  # remove all of this repo's
```

## Configuration

```yaml
lessons:
  enabled: true   # record corrections
  nudge: true     # tell Claude when one is recorded
```
