# Insights

The gate catches problems; insights help **prevent** them. Every Stop gate
run is logged, and `guardrails insights` turns that history into the
numbers that matter and into rules worth teaching Claude up front.

```bash
guardrails insights                  # this repo, last 30 days
guardrails insights --days 90        # a longer window
guardrails insights --all-repos      # every repo on this machine
guardrails insights --json           # for scripts and the skill
```

```text
guardrails insights — last 30 days, this repo

  first-pass rate   62% (31 of 50 turns passed the gate first time)
  blocks per turn   0.6
  hand-offs         2
  sessions          41 (82 gate runs)
  gate time         p50 34.0s, p95 71.2s

Top rules tripped
  RULE                                 SESSIONS  TIMES  EXAMPLE
  lint/errcheck                              14     23  api/handler.go
  coverage                                   11     11
  patch-coverage                              7      7
  lint/SC2086                                 5      9  scripts/deploy.sh

Rule candidates (tripped in many sessions; teach them up front)
  lint/errcheck                        14 sessions → .claude/rules/go.md
  coverage                             11 sessions → .claude/rules/go.md
  patch-coverage                       7 sessions → .claude/rules/go.md
  lint/SC2086                          5 sessions → .claude/rules/shell.md

Run /guardrails:insights in Claude Code to draft these rules for review.
```

## The metrics

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

## Rule candidates

A rule that trips in at least `--min-sessions` sessions (default 5) is a
**rule candidate**: Claude keeps making the same mistake, so the fix
belongs in a `.claude/rules` file that loads before it writes code, not in
another gate failure. Each candidate is mapped to the rules file for its
language (`go.md`, `python.md`, `shell.md`, `yaml.md`, `terraform.md`,
`docker.md`).

`/guardrails:insights` drafts those additions — concrete, imperative,
backed by the counts — and shows them to you as a diff. Rules files are
yours: Guardrails denies Claude editing `.claude/rules/*.md`, so you apply
what you agree with.

## What's recorded

Records are appended to `runs.jsonl` in the plugin data directory
(`${CLAUDE_PLUGIN_DATA}`, or `~/.local/share/guardrails`):

- **Gate runs**: time, session, repository path, whether it passed, the
  attempt number, whether it handed off, the language modules that ran,
  coverage, duration, and each failure's check, rule and file path.
- **Sessions**: when they ended, how many gates ran, how many blocked, and
  how many files were touched.

No source code, test output or prompts are recorded, and **nothing leaves
your machine**: insights read the local file and nothing else. Delete
`runs.jsonl` at any time to start over.
