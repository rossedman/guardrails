---
name: setup
description: Set Guardrails up for this repository — measure where the code is today, recommend a preset (strict, standard or adopt), write .guardrails.yaml and the Claude rules, and offer the status line. Use when the user installs Guardrails, adopts it in an existing repo, asks how far the repo is from the standards, or wants to change how strict it is.
---

# Set up Guardrails for this repository

Guardrails works with no config at all, using the **strict** preset. This
skill picks the right starting point instead, from how the code measures
today.

1. See what's already in place:

   ```bash
   guardrails status --json
   ```

   Note the languages it detected, whether a `.guardrails.yaml` exists, and
   any missing tools or config errors.

2. Measure where the repository is. This changes nothing:

   ```bash
   guardrails gate --all --json
   ```

   Note total coverage, the packages furthest below the bar, and the number
   of lint issues per language. If it takes long, tell the user it's a
   one-time measurement.

3. Recommend a preset and explain the trade-off in two lines:
   - **strict** (90% coverage, 90% of changed lines tested): new or
     already-clean repos. Every turn must meet the full bar.
   - **standard** (80% coverage, 80% of changed lines): repos close to the
     bar, or teams that find 90% too costly.
   - **adopt** (no absolute floor; coverage may never drop, and 80% of the
     lines Claude changes must be tested): existing repos well below the
     bar. "Don't get worse, and test what you touch." Move up later.

   Pick **adopt** if total coverage is more than 10 points below the strict
   bar or there are many lint issues, **standard** if it's within 10, and
   **strict** if it already passes. Mention any single package that would
   dominate the work.

4. On the user's OK, write the config and rules:

   ```bash
   guardrails init --preset adopt     # or standard / strict
   ```

   It writes a short `.guardrails.yaml` (`version: 1` and the preset) and
   path-scoped rules in `.claude/rules/`, skipping files that exist. Add
   `--force` only if the user wants to replace existing ones, and `--full`
   if they want every setting spelled out.

   `init` is the only way you may create these files. Afterwards
   `.guardrails.yaml` and `.claude/rules/*.md` belong to the human:
   Guardrails denies edits to them. If the user wants a key changed, show
   the change as a diff for them to apply.

5. Offer the two optional extras; show them, don't apply them:
   - **Status line.** `guardrails init --statusline` prints a `statusLine`
     snippet with the full path to the binary, for the user to add to
     `~/.claude/settings.json`.
   - **Hard denies.** `init` printed a `permissions.deny` block. Explain in
     one line that it keeps the protections even if a hook fails, and that
     it goes in `.claude/settings.json`.

6. Finish with `guardrails status` and summarize: the preset, what Claude
   will be held to from now on, and when to move up a preset (for adopt:
   once total coverage is within 10 points of the next bar).
