# Go module

The Go module owns `*.go` files and `go.mod`.

| Stage | What runs |
|---|---|
| **On edit: auto-fix** | `goimports -w`, then `gofumpt -w` (gofmt if gofumpt is missing). Generated files are skipped. |
| **On edit: check** | `go vet` and `golangci-lint run --fast-only` on the edited file's package, reporting issues in that file. Type errors anywhere in the package are always reported, because the edit may have broken another file. |
| **At Stop: gate** | `go build ./...`, `go test -race -count=1 -covermode=atomic -coverprofile` on touched packages, coverage checks, full golangci-lint, `go mod tidy -diff`, `govulncheck ./...` |

Packages are grouped by their nearest `go.mod`, so multi-module repositories
are gated module by module.

## Coverage rules

Coverage is the bar that matters most, so it has four parts. All must hold.

**Total.** Statement coverage across touched packages must be at least
`go.coverage.total` (default 90%).

**Per-package floor.** No touched package may fall below
`go.coverage.per_package_floor` (default 80%), so one well-tested package
can't hide an untested one.

**Ratchet.** With `go.coverage.ratchet: true`, no package may end below its
coverage at SessionStart, even in repositories still under 90%. Each passing
gate raises the baseline; it never moves down.

**Patch coverage.** Lines added or changed this session — `git diff` against
the commit at SessionStart, plus untracked files — must be at least
`go.patch_coverage` covered (default 90%). Lines without statements
(comments, declarations) don't count. Failures list the uncovered changed
lines grouped by function:

```text
[patch-coverage] 66.7% of changed lines covered < 90.0% (8/12)
  internal/api/handler.go:94-97  decodeRequest
  fix: test the changed lines above; they are code you wrote this session
```

**Exclusions.** By default `**/mocks/**`, `**/*_mock.go`, `**/*.pb.go`,
`cmd/**/main.go`, and any file with a `// Code generated ... DO NOT EDIT.`
header are excluded. Patterns support `**` and are relative to the module
root.

On failure, the report lists the five functions with the most uncovered
statements, with `file:line`, so Claude knows exactly where to write tests.

## Lint config

If the repository has a golangci-lint config (`.golangci.yml`, `.yaml`,
`.toml` or `.json`) it is used. Otherwise Guardrails uses its bundled
config: the `standard` linters plus bodyclose, errorlint, gocritic, gosec,
misspell, nolintlint, revive, unparam and friends, with gofumpt and
goimports as formatters. `go.lint.config` points at a config elsewhere.

`go.lint.on_edit` controls the per-edit pass: `fast` (default, fast linters
only), `full`, or `off`.

## Toolchain

You need Go itself. Everything around it is
[provisioned automatically](tools.md) at a pinned, checksum-verified version
when it isn't already on your `PATH`.

| Tool | Required | If unavailable |
|---|---|---|
| `go` | yes | The gate fails with a `tools` check |
| `golangci-lint` (v2) | yes | The `lint` check fails, naming the fix |
| `gofumpt` | no | Falls back to `gofmt` |
| `goimports` | no | Imports aren't fixed on edit |
| `govulncheck` | no | The vulnerability check is skipped with a note |

While a tool is still downloading in the background, per-edit checks that
need it are skipped; the Stop gate waits for it.

## Test depth

Once build, tests, coverage and lint pass, the gate goes further on the
functions changed this session: built-in **mutation testing**, required
**benchmarks** for hot paths with **regression checks** against the
session's starting commit, and required **fuzz targets** for parsers with
short fuzz runs. See [Test depth](test-depth.md).
