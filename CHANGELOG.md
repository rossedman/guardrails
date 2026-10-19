# Changelog

Release notes for each version are published on
[GitHub Releases](https://github.com/rossedman/guardrails/releases),
generated from Conventional Commit messages.

## 0.7.0

### Added

- `guardrails status` and `/guardrails:status`: what Guardrails is doing in
  this repo in one view — the languages and their bar, the config, the last
  gate, corrections waiting, how the binary is running (with the `ln -s`
  line to use it from your terminal) — and one suggested next step.
- `/guardrails:setup`: measures the repository, recommends a preset, and
  writes `.guardrails.yaml` and `.claude/rules/`; offers the status line
  and the `permissions.deny` block.
- Presets: `preset: strict | standard | adopt`. strict is the default;
  adopt suits existing repos (no absolute coverage floor, the ratchet, and
  80% of changed lines tested). Any key overrides the preset.
- A one-time message on the first session in a repo saying Guardrails is on,
  which languages it gates and the bar.
- `guardrails learn`: the gate history and your recorded corrections in one
  report (`--json` gives `{"insights": …, "lessons": […]}`), with `--clear`.
- `guardrails init --preset`, `--full` and `--statusline`.

### Changed

- `guardrails init` writes a short `.guardrails.yaml` (`version` and
  `preset`); `--full` writes every setting as before.
- Skills renamed: `/guardrails:check` → `/guardrails:gate`;
  `/guardrails:insights` and `/guardrails:learn` merged into
  `/guardrails:learn`; `/guardrails:baseline` → `/guardrails:setup`.
- CLI help is grouped Daily / Setup / Claude Code. `explain`, `insights`
  and `lessons` still work but are hidden; use `gate --explain` and
  `learn`.
- The docs are organized by task: Get started, Day to day, Tune it,
  Reference. The Insights and Lessons pages are now one Learn page.

## 0.6.0

0.5.0 was tagged but never published, so this release also carries its
changes: the Cobra CLI and the redesigned docs.

### Added

- Dependency check: packages Claude installs (`npm install`, `pip install`,
  `uv add`, `go get`, `cargo add`, `npx` and friends) or adds to a
  manifest (`package.json`, `requirements*.txt`, `pyproject.toml`,
  `go.mod`, `Cargo.toml`) are looked up on deps.dev and OSV first.
  Nonexistent (hallucinated) and malicious packages are denied; typosquats,
  brand-new packages, known vulnerabilities and disallowed licenses ask.
  Configured under `deps:`.
- Lessons: the new UserPromptSubmit hook records your corrections of
  Claude; `guardrails lessons` lists them and `/guardrails:learn` drafts
  them into `.claude/rules/` for you to apply. Configured under `lessons:`.
- The CLI is built on Cobra and Viper: grouped, styled help, `--data-dir`
  and `--config`, `GUARDRAILS_<COMMAND>_<FLAG>` environment overrides and
  shell completion.
- The docs site is rebuilt around a design system.

### Changed

- PreToolUse's timeout is 10s, to leave room for dependency lookups.

## 0.4.0

### Added

- The Go gate tests every package that imports the changed code
  (`[dependents]`), using Go's test cache so only affected packages re-run.
- `guardrails insights` and `/guardrails:insights`: first-pass rate, top
  rules, timeouts and drafted rule additions from the gate history.
- GitHub Action (`uses: rossedman/guardrails@v0.4.0`), a pre-commit hook,
  and `guardrails gate --staged`.

### Changed

- `.claude/rules/*.md` are protected; Claude proposes rule changes as diffs.

### Fixed

- Go test failures show the package path (`a/a_test.go:7`, not `a_test.go:7`).

## 0.3.0

### Added

- `guardrails harden` and the `/guardrails:harden` skill: built-in mutation
  testing of changed Go functions (via `go test -overlay`, skipping
  equivalent boundary mutants), short fuzz runs, and benchmark / fuzz-target
  suggestions.
- Stronger Go rules: test like a mutation tester; when to add benchmarks
  and fuzz targets.

## 0.2.0

### Added

- Python, Shell, YAML, Terraform and Docker modules.
- Tool provisioning: pinned, checksum-verified tools installed on demand
  (archives, PyPI wheels, uv-managed venvs, `go install`), with background
  prefetch at SessionStart, `guardrails tools list|install|path`, and a lock
  generator plus weekly workflow.
- `tools.install` and `tools.prefer` config.

## 0.1.0

### Added

- Go module: gofumpt/goimports on edit, go vet and golangci-lint per
  package, and a Stop gate with build, race tests, total, per-package,
  ratchet and patch coverage, full lint, `go mod tidy` and govulncheck.
- Policy guards for config, suppressions, skipped tests, thresholds,
  generated files, lockfiles, secrets, Terraform state, registry pushes,
  git history and cluster writes.
- Strict layered configuration with a JSON Schema.
- CLI: `gate`, `explain`, `doctor`, `init`, `config`, `schema`,
  `statusline`, `hook`.
- Claude Code plugin with hooks, skills (`check`, `cover`, `baseline`) and
  subagents (`test-writer`, `standards-reviewer`).
