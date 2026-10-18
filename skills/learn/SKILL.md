---
name: learn
description: Turn the corrections the user gave Claude ("don't mock the database", "from now on run make lint") into rules in .claude/rules files, drafted for the human to approve. Use when the user asks to learn from their corrections, turn corrections into rules, or what they've corrected Claude on.
---

# Learn from the user's corrections

When the user corrects Claude or sets a standing rule, Guardrails records it
as a *lesson* (locally, in `lessons.jsonl`). This skill turns those lessons
into rules, so the next session knows them before writing code.

1. Get this repo's lessons:

   ```bash
   guardrails lessons --json
   ```

   Each lesson has an `id`, the `time`, the user's `prompt`, the cues that
   matched (`signals`) and `explicit`: true when the user asked for it to be
   remembered ("from now on", "always", "in this repo we").

2. Group lessons that say the same thing, then sort each group:
   - **A lasting convention**: how this repo does things, true next week
     ("we don't use testify", "run make lint before saying done"). Explicit
     lessons almost always are.
   - **A one-off**: about a specific bug, file or moment ("revert that",
     "no, the other function"). These are not rules; say so briefly.

3. For each convention, **draft** one rule line for the right file:
   - a language or path rule goes in its `.claude/rules/` file (`go.md`,
     `python.md`, `shell.md`, `yaml.md`, `terraform.md`, `docker.md`, or a
     new path-scoped file);
   - general workflow (commits, PRs, what to run before finishing) goes in
     `CLAUDE.md`.

   Make each rule concrete and imperative, in the user's terms, and cite the
   lesson: "Use a real Postgres test container, never mocks, in
   `internal/store` — you corrected this on 2026-10-14." Read the target
   file first; if it already says this, sharpen the existing line instead
   of adding a duplicate.

4. **Show the additions as a diff** for the human to apply, for example:

   ```diff
   --- .claude/rules/go.md
   +++ .claude/rules/go.md
   @@ ## Tests
   +- Use the Postgres test container in internal/store tests; never mock
   +  the database — you corrected this on 2026-10-14 (lesson 3f9a1c0b2e).
   ```

   Never edit `.claude/rules/*.md` yourself: they're human-owned, and
   Guardrails denies the edit. Edit `CLAUDE.md` only if the user asks you to.

5. Once the human has applied or rejected each group, clear the lessons you
   handled (including the one-offs you set aside) so they aren't proposed
   again:

   ```bash
   guardrails lessons --clear --id 3f9a1c0b2e --id 7c21d04e9a
   ```
