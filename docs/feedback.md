# Feedback format

Every blocking message follows one template so Claude can parse it the same
way every time.

```text
guardrails: go gate failed (2 of 6 checks)

[coverage] 84.2% < 90.0% (touched: ./internal/sched, ./internal/api)
  internal/sched/queue.go:41  (*Queue).Drain  0/14 stmts
  internal/api/handler.go:88  decodeRequest  3/11 stmts
  fix: add table-driven tests for the functions above

[lint] 2 issues
  internal/api/handler.go:102:5  errcheck  Error return value of `w.Write` is not checked
  internal/sched/queue.go:17:1  revive  exported type Queue should have comment
  fix: fix each issue; suppression comments are not allowed

attempt 1 of 3 — run `guardrails gate --explain` for full output
```

## Rules

**Order.** The failing check's name in brackets with a one-line summary,
then `file:line:col  rule  message` per issue, then one `fix:` hint for the
group.

**Caps.** At most 10 issues per check and 4,000 characters in total. The rest
is written to a report file in the session directory and the message ends
with a pointer to it. `guardrails gate --explain` runs the gate and prints the full report.

**Dedup.** Per-edit issues already reported this session are summarized as a
count (`(3 previously reported, unchanged)`) instead of repeated.

**Channels.**

| Channel | Used for |
|---|---|
| `decision: "block"` + `reason` | PostToolUse and Stop failures Claude must fix |
| `permissionDecision` + reason | PreToolUse guards |
| `additionalContext` | The standards summary at SessionStart |
| `systemMessage` | Things only you need: missing tools, config errors, retries exhausted, warn-mode reports |

**Tone.** Factual statements, no imperative "SYSTEM:" framing, which can
trip prompt-injection defenses.
