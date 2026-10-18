---
name: baseline
description: Report existing quality debt in the repository (lint issue count, coverage gaps by package) and propose a ratchet plan, without changing any code. Use when adopting Guardrails in an existing repo or when the user asks how far the repo is from the standards.
---

# Baseline the repository

This skill is read-only. Do not edit files.

1. Run `guardrails doctor` and note any missing tools.
2. Run `guardrails gate --all --json` and capture total coverage, failing
   packages and lint issue counts.
3. Run `go test -cover ./...` to get per-package coverage, and
   `golangci-lint run --output.json.path=stdout ./...` to count issues by
   linter.
4. Report:
   - Total and per-package coverage against `go.coverage.total` and
     `go.coverage.per_package_floor` (`guardrails config` shows the values).
   - Lint issues grouped by linter, with the three noisiest files.
   - A ratchet plan: which packages to bring up first (most-edited, lowest
     coverage), a realistic interim floor, and whether `mode: warn` makes
     sense for any module while debt is paid down.
5. Present config changes as a proposed `.guardrails.yaml` diff for the
   human to apply. The file is protected; you cannot edit it.
