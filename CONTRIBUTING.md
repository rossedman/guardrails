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

1. Set the version everywhere it's pinned (the plugin manifest, which the
   launcher uses to pick the binary, the install snippets in the README and
   docs, and the changelog's Unreleased heading), then open a PR:
   `make version VERSION=X.Y.Z`.
2. Merge to `main`, then tag the merge commit with the same version:
   `git tag vX.Y.Z && git push origin vX.Y.Z`.
3. The release workflow verifies the plugin version matches the tag and
   runs GoReleaser. A tag pushed without step 1 fails that check; bump,
   merge, and tag again.

## Regenerating the graphics

The README header, social preview and terminal image are HTML pages in
`docs/assets/src/`, styled with the design tokens in `brand.css` (the site
uses the same tokens in `docs/assets/site/landing.css`). Render them to PNG
with headless Chrome or Chromium:

```bash
make header    # or: docs/assets/render.sh [banner|social-preview|terminal]
```
