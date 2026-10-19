# Companion tools

Hooks enforce; these teach Claude the standards up front so it hits the gate
less often. They ship in the same plugin.

## Skills

| Skill | What it does |
|---|---|
| `/guardrails:status` | What Guardrails is doing in this repo — the languages and their bar, the last gate, corrections waiting — and the one thing to do next, in plain words. Runs [`guardrails status`](cli.md#guardrails-status). |
| `/guardrails:setup` | Measures where the repo is today (coverage, lint), recommends a [preset](presets.md), and on your OK writes `.guardrails.yaml` and `.claude/rules/`. Offers the status line and the `permissions.deny` block. |
| `/guardrails:gate` | Runs the full gate on demand and explains the results, with the smallest set of changes that would make it pass. Useful before you commit. |
| `/guardrails:cover` | Reads the coverage report, lists the biggest gaps, and writes behavior-asserting, table-driven tests until the threshold is met. |
| `/guardrails:harden` | Mutation-tests and fuzzes the code you changed with [`guardrails harden`](harden.md), adds the test cases that kill surviving mutants, and reports back. |
| `/guardrails:learn` | Turns what Claude keeps getting wrong into rules: the checks the gate trips most and the corrections you've given Claude, from [`guardrails learn`](learn.md), drafted as `.claude/rules/*.md` additions for you to approve. |

## Subagents

| Agent | What it does |
|---|---|
| `test-writer` | Writes tests only. Guardrails denies its edits to anything but `*_test.go`, `test_*.py`, `*_test.py` and `testdata/`, and runs the gate on its work at SubagentStop. |
| `standards-reviewer` | Read-only review of a diff against `.claude/rules/*.md` for what linters can't catch: naming, API shape, error messages, missing edge-case tests. |

## Rules

`guardrails init` (which `/guardrails:setup` runs) installs `.claude/rules/go.md`, a path-scoped rule that
loads only when Claude works on Go files: error wrapping, context first,
no global state, table-driven tests that would catch a mutant, and when to
add benchmarks and fuzz targets. Plugins can't ship rules directly, which is why `init` writes them
into the repo, where you can edit them. Claude can't: rules are protected
like the rest of the guardrail config, and `/guardrails:learn` proposes
changes as diffs instead.

## Status line

`guardrails statusline` shows the last gate result, coverage against the
threshold, and the lint count. See
[Getting started](getting-started.md#status-line-optional).
