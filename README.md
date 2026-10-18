<p align="center">
  <img src="docs/assets/header.png" alt="guardrails — quality gates for Claude Code" width="100%">
</p>

<p align="center">
  <a href="https://github.com/rossedman/guardrails/actions/workflows/ci.yml"><img src="https://github.com/rossedman/guardrails/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/rossedman/guardrails/releases"><img src="https://img.shields.io/github/v/release/rossedman/guardrails?sort=semver&color=F5B83D" alt="Release"></a>
  <a href="https://pkg.go.dev/github.com/rossedman/guardrails"><img src="https://pkg.go.dev/badge/github.com/rossedman/guardrails.svg" alt="Go Reference"></a>
  <a href="https://goreportcard.com/report/github.com/rossedman/guardrails"><img src="https://goreportcard.com/badge/github.com/rossedman/guardrails" alt="Go Report Card"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-131C38" alt="MIT License"></a>
</p>

<p align="center">
  <b><a href="https://rossedman.github.io/guardrails/">Documentation</a></b> ·
  <a href="https://rossedman.github.io/guardrails/getting-started/">Getting started</a> ·
  <a href="https://rossedman.github.io/guardrails/configuration/">Configuration</a> ·
  <a href="https://rossedman.github.io/guardrails/guards/">Policy guards</a>
</p>

---

**Guardrails makes Claude meet your standards before it can call work done.**
It's one Go binary, shipped as a [Claude Code](https://claude.com/claude-code)
plugin, with a fast inner loop on every edit and a hard gate at the end of
every turn.

- **Fix, don't nag.** Every file Claude edits is formatted in place
  (gofumpt, goimports), then its package is linted (go vet, golangci-lint),
  in about two seconds.
- **Gate every turn.** Before Claude can stop, touched packages must build,
  pass `go test -race`, reach **90% coverage** with a per-package floor and
  a ratchet, cover **90% of the lines changed this session**, lint clean,
  keep `go.mod` tidy and pass govulncheck.
- **Claude can't move the goalposts.** New `//nolint`, `# noqa`, `t.Skip`,
  lowered thresholds and edits to the guardrail config are denied before
  they happen.
- **Dangerous commands need you.** `terraform apply`, force pushes, pushes
  to `main` and `--no-verify` are denied; registry pushes and non-dev
  cluster writes ask first.
- **Hands off, not stuck.** After three consecutive blocks the gate stops,
  summarizes what's left, and hands the turn back to you.

## What Claude sees

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

## Quick start

Install the toolchain Guardrails orchestrates (Go and
[golangci-lint v2](https://golangci-lint.run/welcome/install/) are required;
gofumpt, goimports and govulncheck are recommended), then in Claude Code:

```text
/plugin marketplace add rossedman/guardrails
/plugin install guardrails@guardrails
```

The plugin downloads the matching release binary on first use and verifies
its checksum. Optionally, tune it for a repository:

```bash
go install github.com/rossedman/guardrails/cmd/guardrails@latest
guardrails doctor   # check tools and config
guardrails init     # write .guardrails.yaml and .claude/rules/go.md
guardrails gate     # run the same gate Claude faces, on your changes
```

## How it works

| Event | What runs | On failure |
|---|---|---|
| **SessionStart** | Detect toolchains, inject the repo's standards and coverage baseline | Context for Claude; missing tools reported to you |
| **PreToolUse** | [Policy guards](https://rossedman.github.io/guardrails/guards/) on edits and shell commands | `deny` or `ask`, with a reason |
| **PostToolUse** | Auto-format, then fast lint on the edited package | Block with a `file:line` report |
| **Stop** | Build, race tests, coverage, patch coverage, full lint, tidy, govulncheck | Block; after 3 attempts, hand off to you |
| **SubagentStop** | The same gate, scoped to the subagent's files | Same as Stop |
| **SessionEnd** | Append a run record to `runs.jsonl` | — |

Read more in [How it works](https://rossedman.github.io/guardrails/how-it-works/).

## Configuration

Zero config works. To tune, add `.guardrails.yaml` (Claude can't edit it):

```yaml
version: 1
mode: enforce            # enforce | warn
go:
  coverage:
    total: 90
    per_package_floor: 80
    ratchet: true
  patch_coverage: 90
  lint:
    on_edit: fast        # fast | full | off
gate:
  max_consecutive_blocks: 3
```

Unknown keys are an error, so a typo can never silently disable a check.
See the [configuration reference](https://rossedman.github.io/guardrails/configuration/).

## Status

Guardrails is **early (v0.1)**. The Go module and all policy guards are
implemented; Python, Shell, YAML, Terraform and Docker modules, mutation
testing, benchmarks and fuzzing are on the
[roadmap](https://rossedman.github.io/guardrails/roadmap/). Guardrails
holds itself to its own gate in CI.

## Contributing

Contributions are welcome — see [CONTRIBUTING.md](CONTRIBUTING.md). Please
report security issues privately as described in [SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE) © Ross Edman
