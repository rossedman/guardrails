# Contributing to Guardrails

Thanks for helping make Claude's work better. Bug reports, ideas, docs and
code are all welcome.

## Ground rules

- Be kind. This project follows the [Code of Conduct](https://github.com/rossedman/guardrails/blob/main/CODE_OF_CONDUCT.md).
- For anything bigger than a small fix, open an issue first so we can agree
  on the approach.
- Security issues go through [SECURITY.md](https://github.com/rossedman/guardrails/blob/main/SECURITY.md), not public issues.

## Development setup

You need Go (see `go.mod` for the version), [golangci-lint
v2](https://golangci-lint.run/welcome/install/), and ideally gofumpt and
govulncheck.

```bash
git clone https://github.com/rossedman/guardrails
cd guardrails
make test      # go test -race ./...
make lint      # golangci-lint run
make gate      # build the binary and run its own gate on itself
make harden    # mutation-test and fuzz what this branch changed
```

To try your build inside Claude Code, point the plugin launcher at it:

```bash
make build
export GUARDRAILS_BIN="$PWD/dist/guardrails"
claude
```

Then add your checkout as a local marketplace and install from it:

```text
/plugin marketplace add ./
/plugin install guardrails@guardrails
```

## The bar

Guardrails holds itself to its own gate, and CI enforces it:

- `go test -race ./...` passes on Linux and macOS.
- Total coverage stays at or above **90%**, with no package below 80%.
- `golangci-lint run` is clean. No `//nolint` without a specific linter and
  a reason.
- Code is gofumpt-formatted.

Tests should assert behavior, not just execute code; `make harden`
mutation-tests your changes to prove it. Prefer table-driven
tests. Hooks are pure functions from stdin JSON to stdout JSON; the engine's
golden files live in `internal/engine/testdata` and are regenerated with
`go test ./internal/engine -update`.

## Adding a language module

1. Create `internal/module/<lang>` implementing `module.Module`
   (`Match`, `Fix`, `Check`, `Gate`, `Tools`).
2. Register it in `engine.New`.
3. Add its config block to `internal/config` and `schema.json` (the config
   tests fail if the schema and defaults drift apart).
4. Add tests with `runner.Fake`, plus a real-tool integration test that
   skips when the tool is missing.
5. Document it in `docs/` and add a rule file under
   `internal/assets/rules/`.

## Commit messages

Use [Conventional Commits](https://www.conventionalcommits.org/):
`feat:`, `fix:`, `docs:`, `test:`, `chore:`. Release notes are generated
from them.

## Releasing

Maintainers:

1. Bump `version` in `.claude-plugin/plugin.json` (the launcher downloads
   the binary matching it).
2. Merge to `main`, then tag the same version:
   `git tag vX.Y.Z && git push origin vX.Y.Z`.
3. The release workflow verifies the plugin version matches the tag and
   runs GoReleaser.

## Regenerating the header image

```bash
pip install cairosvg
python3 scripts/header.py
```
