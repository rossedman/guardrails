---
name: learn
description: Turn what Claude keeps getting wrong into rules — the checks the Guardrails gate trips most, and the corrections the user gave Claude ("don't mock the database", "from now on run make lint") — drafted as .claude/rules additions for the human to approve. Use when the user asks how the gate is going, what Claude keeps getting wrong, how to reduce gate failures, or to learn from their corrections.
---

# Learn from the gate and from the user's corrections

Guardrails keeps two local records: every Stop gate run (`runs.jsonl`) and
every correction or standing instruction the user gave Claude (lessons, in
`lessons.jsonl`). This skill turns both into rules, so the next session
meets the bar the first time instead of being caught by the gate or
corrected again.

1. Get the data:

   ```bash
   guardrails learn --json                 # this repo, last 30 days
   guardrails learn --json --days 90       # a longer window
   guardrails learn --json --all-repos     # every repo on this machine
   ```

   The result has two parts:
   - `insights`: the gate history — `first_pass_rate`, `top_rules`,
     `rule_candidates` (rules tripped in at least `--min-sessions`
     sessions, default 5; lower it if the history is short), timeouts.
   - `lessons`: the recorded corrections, each with an `id`, the `time`,
     the user's `prompt`, the cues that matched (`signals`) and `explicit`:
     true when the user asked for it to be remembered ("from now on",
     "always", "in this repo we").

2. Explain the trend in a few lines:
   - **First-pass rate** is the success metric: the share of turns that
     passed the Stop gate on the first try. It should trend up.
   - Name the top three rules from `top_rules` with their session counts,
     and how many corrections are waiting.

3. Sort the corrections. Group lessons that say the same thing, then:
   - **A lasting convention**: how this repo does things, true next week
     ("we don't use testify", "run make lint before saying done"). Explicit
     lessons almost always are.
   - **A one-off**: about a specific bug, file or moment ("revert that",
     "no, the other function"). These are not rules; say so briefly.

4. **Draft** one rule line for each rule candidate and each convention:
   - A rule candidate goes in the rules file it names (`rules_file`, under
     `.claude/rules/`). A candidate with no `rules_file` (language unknown)
     is a suggestion to discuss, not a diff.
   - A convention goes in its language or path rules file (`go.md`,
     `python.md`, `shell.md`, `yaml.md`, `terraform.md`, `docker.md`, or a
     new path-scoped file); general workflow (commits, PRs, what to run
     before finishing) goes in `CLAUDE.md`.

   Make each rule concrete and imperative, in the user's terms, and cite
   its evidence: "Check the error returned by `w.Write` — errcheck flagged
   this in 14 sessions", or "Use the Postgres test container, never mocks,
   in `internal/store` — you corrected this on 2026-10-14." Read the target
   file first; if it already says this, sharpen the existing line instead
   of adding a duplicate.

5. **Show the additions as a diff** for the human to apply, for example:

   ```diff
   --- .claude/rules/go.md
   +++ .claude/rules/go.md
   @@ ## Code
   +- Check every error, including from `w.Write` and `Close` — errcheck
   +  flagged unchecked errors in 14 sessions (api/handler.go, store/db.go).
   +- Use the Postgres test container in internal/store tests; never mock
   +  the database — you corrected this on 2026-10-14 (lesson 3f9a1c0b2e).
   ```

   Never edit `.claude/rules/*.md` yourself: they're human-owned, and
   Guardrails denies the edit. Edit `CLAUDE.md` only if the user asks you to.

6. If a check keeps timing out, suggest the config change instead
   (`gate.timeout_seconds`, or a narrower test scope) as a proposed
   `.guardrails.yaml` diff — also the human's to apply.

7. Once the human has applied or rejected each group, clear the corrections
   you handled (including the one-offs you set aside) so they aren't
   proposed again:

   ```bash
   guardrails learn --clear --id 3f9a1c0b2e --id 7c21d04e9a
   ```
