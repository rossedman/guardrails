---
hide:
  - navigation
  - toc
---

<div class="hero" markdown>
![Guardrails — quality gates for Claude Code](assets/header.png)
</div>

# Guardrails

**Guardrails makes Claude meet your standards before it can call work done.**
It is one Go binary, shipped as a Claude Code plugin, that formats and lints
every file Claude edits, blocks the end of each turn until tests pass and
coverage holds, and stops Claude from weakening the standards to get there.

[Get started](getting-started.md){ .md-button .md-button--primary }
[How it works](how-it-works.md){ .md-button }

<div class="grid cards" markdown>

-   :material-lightning-bolt:{ .lg .middle } __Fast inner loop__

    ---

    After every edit: gofumpt and goimports fix the file in place, then
    `go vet` and golangci-lint check its package, all in under two seconds.
    Claude hears only about problems it has to reason about.

-   :material-gate:{ .lg .middle } __Hard gate at the end of each turn__

    ---

    At Stop, touched packages must build, pass `go test -race`, hit 90%
    coverage, cover the lines changed this session, and lint clean. If not,
    Claude keeps working with a precise, `file:line`-first report.

-   :material-shield-lock:{ .lg .middle } __Claude can't move the goalposts__

    ---

    New `//nolint`, `# noqa`, `t.Skip`, lowered thresholds, and edits to
    `.guardrails.yaml` or `.golangci.yml` are denied before they happen.

-   :material-alert-octagon:{ .lg .middle } __Blast-radius guards__

    ---

    `terraform apply`, force pushes, pushes to `main` and `--no-verify` are
    denied. Registry pushes and writes to non-dev clusters ask you first.

-   :material-chart-line:{ .lg .middle } __Coverage ratchet__

    ---

    Coverage is measured on what Claude touched, with a per-package floor and
    a ratchet: a package can never end a session below where it started.

-   :material-robot-happy:{ .lg .middle } __Hands off, not stuck__

    ---

    After three consecutive blocks the gate stops blocking, summarizes what's
    still failing, and hands the turn back to you.

</div>

## What Claude sees

When the gate fails, Claude gets one message in a fixed format it can act on:

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

## Install

```text
/plugin marketplace add rossedman/guardrails
/plugin install guardrails@guardrails
```

The plugin downloads the matching release binary on first use and verifies
its checksum. See [Getting started](getting-started.md) for the toolchain
it expects and how to tune it per repository.
