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
  <a href="https://rossedman.github.io/guardrails/daily/">Day to day</a> ·
  <a href="https://rossedman.github.io/guardrails/presets/">Presets</a>
</p>

---

**Guardrails makes Claude meet your standards before it can call work done.**
It's one Go binary, shipped as a [Claude Code](https://claude.com/claude-code)
plugin, with a fast inner loop on every edit and a hard gate at the end of
every turn — for **Go, Python, Shell, YAML, Terraform and Docker**.

- **Fix, don't nag.** Every file Claude edits is formatted in place
  (gofumpt, ruff, shfmt, terraform fmt), then linted and type-checked, in
  about two seconds.
- **Gate every turn.** Before Claude can stop, touched code must build and
  pass its tests, meet your coverage bar — **90%** with a ratchet, and 90%
  of the lines changed this session, in the default preset — and pass every
  linter and scanner for its language.
- **A bar that fits the repo.** Pick a preset: **strict** for new code,
  **standard** a notch lower, or **adopt** for existing repos — coverage may
  never drop, and what Claude changes must be tested.
- **Tests that test.** `guardrails harden` (or `/guardrails:harden`)
  mutation-tests the Go code you changed and fuzzes its fuzz targets, so
  high coverage can't hide assertion-free tests.
- **Nothing to install.** ruff, mypy, shellcheck, yamllint, tflint, trivy,
  golangci-lint and the rest are provisioned automatically:
  pinned versions, checksum-verified, downloaded on first use.
- **Claude can't move the goalposts.** New `//nolint`, `# noqa`, `t.Skip`,
  lowered thresholds and edits to the guardrail config are denied before
  they happen.
- **Dangerous commands need you.** `terraform apply`, force pushes, pushes
  to `main` and `--no-verify` are denied; registry pushes and non-dev
  cluster writes ask first.
- **No phantom packages.** Before Claude installs or adds a dependency
  (npm, PyPI, Go, Cargo), it's looked up: names that don't exist,
  typosquats and malicious packages are denied; brand-new packages, known
  vulnerabilities and disallowed licenses ask first.
- **It learns.** When you correct Claude ("no, we don't mock the
  database"), the correction is recorded; `/guardrails:learn` turns your
  corrections and the checks Claude trips most into rules, so the next
  session already knows.
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

In Claude Code:

```text
/plugin marketplace add rossedman/guardrails
/plugin install guardrails@guardrails
```

That's it — Guardrails tells you it's on at the start of your next session,
and which languages and bar it's holding Claude to. Then pick the bar for
this repository:

```text
/guardrails:setup
```

Claude measures where the code is today, recommends a
[preset](https://rossedman.github.io/guardrails/presets/), and writes a
two-line `.guardrails.yaml` and the `.claude/rules/` that teach Claude your
standards. Day to day, four commands cover it:

| Ask Claude | For |
|---|---|
| `/guardrails:status` | What's on, the last gate, and the one thing to do next |
| `/guardrails:gate` | Run the gate now and explain it, before you commit |
| `/guardrails:harden` | Check the tests really test what you changed |
| `/guardrails:learn` | Turn what Claude keeps getting wrong into rules |

The plugin downloads its own binary and the
[tools it runs](https://rossedman.github.io/guardrails/tools/) on first use,
each pinned and checksum-verified; tools already on your `PATH` are used
first.

### From a terminal

The same commands work as `guardrails status`, `gate`, `harden` and `learn`.
Inside Claude Code the binary is already on `PATH`; for your own shell,
`guardrails status` prints the `ln -s` line that links the plugin's binary,
or install it with Go:

```bash
go install github.com/rossedman/guardrails/cmd/guardrails@latest
```

## How it works

| Event | What runs | On failure |
|---|---|---|
| **SessionStart** | Detect languages, prefetch their tools, inject the standards and coverage baseline | Context for Claude |
| **UserPromptSubmit** | Record your corrections of Claude as [lessons](https://rossedman.github.io/guardrails/learn/) | Context for Claude |
| **PreToolUse** | [Policy guards](https://rossedman.github.io/guardrails/guards/) and the [dependency check](https://rossedman.github.io/guardrails/dependencies/) on edits and shell commands | `deny` or `ask`, with a reason |
| **PostToolUse** | Auto-format, then fast lint and type checks on the edited file | Block with a `file:line` report |
| **Stop** | Build, tests, coverage, patch coverage, full lint, scanners, per language | Block; after 3 attempts, hand off to you |
| **SubagentStop** | The same gate, scoped to the subagent's files | Same as Stop |
| **SessionEnd** | Append a run record to `runs.jsonl` | — |

| Language | On edit | At Stop |
|---|---|---|
| [Go](https://rossedman.github.io/guardrails/go/) | gofumpt, goimports, go vet, golangci-lint | build, `go test -race` on touched packages and their dependents, coverage, golangci-lint, `go mod tidy`, govulncheck |
| [Python](https://rossedman.github.io/guardrails/python/) | ruff format + fix, ruff, mypy | pytest + coverage, mypy, ruff, `uv lock --check` |
| [Shell](https://rossedman.github.io/guardrails/shell/) | shfmt, shellcheck | shellcheck, shfmt diff |
| [YAML](https://rossedman.github.io/guardrails/yaml/) | yamllint, actionlint, kubeconform | the same, over every touched file |
| [Terraform](https://rossedman.github.io/guardrails/terraform/) | terraform fmt, tflint | init + validate, tflint, trivy, provider version constraints |
| [Docker](https://rossedman.github.io/guardrails/docker/) | built-in Dockerfile checks | built-in checks, digest-pinned bases, non-root `USER`, `docker build --check` |

Read more in [How it works](https://rossedman.github.io/guardrails/how-it-works/).

## Same gate in CI and pre-commit

```yaml
# .github/workflows/guardrails.yml
- uses: actions/checkout@v5
  with: { fetch-depth: 0 }
- uses: rossedman/guardrails@v0.7.0
```

```yaml
# .pre-commit-config.yaml
repos:
  - repo: https://github.com/rossedman/guardrails
    rev: v0.7.0
    hooks:
      - id: guardrails
```

Same binary, same config, same result as Claude's hooks. See
[CI and pre-commit](https://rossedman.github.io/guardrails/ci/).

## Configuration

Zero config works: the **strict** preset applies. To tune, add
`.guardrails.yaml` (`/guardrails:setup` writes it; Claude can't edit it
afterwards):

```yaml
version: 1
preset: adopt            # strict | standard | adopt
go:
  patch_coverage: 90     # any key overrides the preset
```

Unknown keys are an error, so a typo can never silently disable a check.
See [presets](https://rossedman.github.io/guardrails/presets/) and the
[configuration reference](https://rossedman.github.io/guardrails/configuration/).

## Status

Guardrails is **early (v0.7)**. All six language modules, tool
provisioning, the policy guards, the dependency check, on-demand hardening
(mutation testing and fuzzing) and learning from the gate and your
corrections are implemented; Python mutation testing and benchmark
regression checks are next on the
[roadmap](https://rossedman.github.io/guardrails/roadmap/). Guardrails
holds itself to its own gate in CI.

## Contributing

Contributions are welcome — see [CONTRIBUTING.md](CONTRIBUTING.md). Please
report security issues privately as described in [SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE) © Ross Edman
