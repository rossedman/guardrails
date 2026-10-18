---
name: insights
description: Summarize how Claude has done against the Guardrails gate — first-pass rate, the rules it trips most, timeouts — and draft additions to .claude/rules files for the human to approve. Use when the user asks how the gate is going, what Claude keeps getting wrong, or how to reduce gate failures.
---

# Learn from the gate's history

Every Stop gate run is logged locally to `runs.jsonl`. This skill turns that
history into better rules, so Claude meets the bar the first time instead
of being caught by the gate.

1. Get the data:

   ```bash
   guardrails insights --json                 # this repo, last 30 days
   guardrails insights --json --days 90       # a longer window
   guardrails insights --json --all-repos     # every repo on this machine
   ```

   Lower `--min-sessions` (default 5) if the history is short.

2. Explain the trend to the user in a few lines:
   - **First-pass rate** (`first_pass_rate`) is the success metric: the
     share of turns that passed the Stop gate on the first try. It should
     trend up over time.
   - Blocks per turn and hand-offs show how much back-and-forth the gate
     causes; timeouts and p95 gate time point at slow checks.
   - Name the top three rules from `top_rules` with their session counts.

3. For each entry in `rule_candidates`, **draft** one addition to the rules
   file it names (`rules_file`, under `.claude/rules/`). Make each rule:
   - concrete and imperative ("Check the error returned by `w.Write`");
   - specific to the failing rule and the example files, not generic advice;
   - backed by the count, e.g. "— errcheck flagged this in 14 sessions".

   Read the current rules file first and don't duplicate what it already
   says; sharpen the existing line instead.

4. **Show the additions as a diff** for the human to apply, for example:

   ```diff
   --- .claude/rules/go.md
   +++ .claude/rules/go.md
   @@ ## Code
   +- Check every error, including from `w.Write` and `Close` — errcheck
   +  flagged unchecked errors in 14 sessions (api/handler.go, store/db.go).
   ```

   Never edit `.claude/rules/*.md` yourself: they're human-owned, and
   Guardrails denies the edit. A candidate with no `rules_file` (language
   unknown) is a suggestion to discuss, not a diff.

5. If a check keeps timing out, suggest the config change instead
   (`gate.timeout_seconds`, or a narrower test scope) as a proposed
   `.guardrails.yaml` diff — also the human's to apply.
