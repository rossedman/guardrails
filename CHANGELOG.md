# Changelog

Release notes for each version are published on
[GitHub Releases](https://github.com/rossedman/guardrails/releases),
generated from Conventional Commit messages.

## Unreleased

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
