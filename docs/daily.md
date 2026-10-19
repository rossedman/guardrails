# Day to day

Once Guardrails is on, you work with Claude as usual. Here is what happens
along the way, and the four things you might ask for.

## What happens while Claude works

**Every edit is fixed and checked.** When Claude edits a file, Guardrails
formats it in place (gofumpt, ruff, shfmt, terraform fmt) and runs fast lint
and type checks on it, in about two seconds. Claude only hears about
problems it has to reason about, as a short `file:line` report.

**Risky actions are stopped first.** Before an edit or a shell command runs,
the [policy guards](guards.md) and the [dependency check](dependencies.md)
look at it:

- **denied**, with the reason shown to Claude: weakening the standards (new
  `//nolint`, `t.Skip`, lowered thresholds, edits to `.guardrails.yaml`),
  irreversible operations (`terraform apply`, force pushes), packages that
  don't exist or are malicious;
- **asks you first**: registry pushes, writes to non-dev clusters, brand-new
  packages, known vulnerabilities.

**The gate runs when Claude stops.** Before a turn can end, the code Claude
touched must build, pass its tests, meet the coverage bar of your
[preset](presets.md), and pass every linter and scanner for its language.
If it doesn't, Claude gets a short report of [what failed and
where](feedback.md) and keeps working.

**You get the turn back after three tries.** If the gate blocks three times
in a row, it stops blocking, summarizes what's left for you, and lets the
turn end. Nothing loops forever.

**Your corrections are remembered.** When you tell Claude "no, we don't do
it that way", Guardrails records it, and Claude follows it for the rest of
the session. See [Learn](learn.md).

## The four things you do

| Ask Claude | What it does |
|---|---|
| `/guardrails:status` | What's on in this repo, the bar, the last gate, anything waiting, and the one thing to do next. |
| `/guardrails:gate` | Runs the full gate now and explains it, with the smallest set of changes that would pass. Useful before you commit. |
| `/guardrails:harden` | Checks that the tests behind your changes actually assert something: [mutation testing and fuzzing](harden.md), then adds the missing cases. |
| `/guardrails:learn` | Turns what Claude keeps getting wrong — from the gate and from your corrections — into rules for `.claude/rules/`, as a diff for you to apply. See [Learn](learn.md). |

Two more for specific jobs: `/guardrails:setup` to pick or change the
[preset](presets.md), and `/guardrails:cover` to write the tests a coverage
gap needs. See [Companion tools](companion-tools.md).

## The same from a terminal

```bash
guardrails status              # what's on and what to do next
guardrails gate                # files changed against HEAD
guardrails gate --all          # every package
guardrails gate --explain      # the full, untruncated report
guardrails harden --base main  # mutation-test and fuzz the whole branch
guardrails learn               # gate history and your corrections
```

See [From a terminal](getting-started.md#from-a-terminal-optional) to get
the command, and the [CLI](cli.md) for every flag.
