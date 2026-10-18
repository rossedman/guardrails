---
name: check
description: Run the full Guardrails quality gate on demand and explain the results. Use before committing, when the user asks whether the work meets the standards, or to see why the Stop gate is blocking.
---

# Run the quality gate

1. Run `guardrails gate --explain` from the repository root. It gates files
   changed against `HEAD` plus untracked files. Use `guardrails gate --all
   --explain` when the user asks about the whole repository.
2. Read the report. Each failing check is a `[name]` header followed by
   `file:line` locations and one `fix:` hint.
3. Summarize for the user in this order: what passed, what failed and why,
   and the smallest set of changes that would make the gate pass.
4. If the user asked you to fix the failures, fix them in order: `build`,
   `tests`, `coverage` / `patch-coverage`, `lint`, `mod-tidy`, `vuln`.
   Re-run the gate after each group.

Never add suppression comments, skip tests or edit `.guardrails.yaml` /
`.golangci.yml` to get green; those edits are denied and the human owns them.
If a check looks wrong, say so and propose the config change in chat.
