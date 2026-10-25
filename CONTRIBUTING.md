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
make lint      # golangci-lint, one command per file, doc comments
make cover     # the 90% coverage gate on hand-written code
make check     # everything CI checks, before you push
make gate      # build the binary and run its own gate on itself
make harden    # mutation-test and fuzz what this branch changed
make bench     # benchmarks into bench.txt, for benchstat
make fuzz      # every fuzz target for FUZZTIME (default 30s)
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

### Scanner feedback with sarifix

`sarifix.yaml` configures [sarifix](https://github.com/rossedman/sarifix)
with golangci-lint and gitleaks. Run `sarifix check` to see the findings
your uncommitted changes introduce. With the
[sarifix plugin](https://github.com/rossedman/claude/tree/main/plugins/sarifix)
installed in Claude Code, the same check runs before Claude finishes a turn.
The `sarifix` workflow runs it on pull requests, and on `main` uploads to
code scanning.

## The bar

Guardrails holds itself to its own gate, and CI enforces it:

- `go test -race ./...` passes on Linux and macOS.
- Total coverage stays at or above **90%**, with no package below 80%.
- `golangci-lint run` is clean. No `//nolint` without a specific linter and
  a reason.
- Code is gofumpt-formatted.
- Every function and method, exported or not, has a doc comment starting
  with its name, and every package has a package comment
  (`go run ./tools/doccheck ./...`).
- Each file in `internal/cli` defines at most one cobra command
  (`tools/check-cmds.sh`).
- Every critical path below has a benchmark, and every one that takes
  outside input has a fuzz target that runs 30s clean in CI.

Tests should assert behavior, not just execute code; `make harden`
mutation-tests your changes to prove it. Prefer table-driven
tests. Hooks are pure functions from stdin JSON to stdout JSON; the engine's
golden files live in `internal/engine/testdata` and are regenerated with
`go test ./internal/engine -update`.

## Critical paths

These functions parse input Guardrails doesn't control, run in hot loops,
or resolve configuration. Each has a `Benchmark` and, where it takes outside
input, a `Fuzz` target that asserts properties (determinism, invariants),
not just "didn't crash". Add to this list when you add code like this, and
commit any failing fuzz input under the package's `testdata/fuzz/`.

| Path | Why | Bench | Fuzz |
| --- | --- | --- | --- |
| `hook.ReadInput`, `Input.Tool` | every hook event's JSON from Claude Code | yes | yes |
| `guard.CheckBash`, `guard.Segments` | every Bash command, before it runs | yes | yes |
| `guard.CheckFile`, `guard.IsProtected` | every edit, before it lands | yes | yes |
| `lessons.Detect` | every prompt the user sends | yes | yes |
| `glob.Match`, `glob.Any` | every file against every pattern | yes | yes |
| `deps.FromCommand` | every Bash command, for package installs | yes | yes |
| `deps.FromManifest` | every manifest edit | yes | yes |
| `deps.Suggest` | typosquat check against the popular-package lists | yes | yes |
| `config.Merge`, `config.Load` | config resolution from user and repo files | yes | `Merge` |
| `coverage.Parse` | every Go coverage profile | yes | yes |
| `diff.ParseUnified` | every `git diff` the gate reads | yes | yes |
| `docker.Parse` | every Dockerfile edited or gated | yes | yes |
| mypy output parsing (`python`) | every mypy run | yes | yes |
| `feedback.Render` | every gate report shown to Claude | yes | yes |

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

## Pull requests

- Every change to `main` goes through a pull request. It needs an approval
  from a code owner (`.github/CODEOWNERS`), every CI check green on an
  up-to-date branch, and all review conversations resolved.
- Pull requests are squash-merged, so the PR title becomes the commit
  message: write it as a Conventional Commit.
- The branch and tag rules live in `.github/rulesets/` and are reviewed like
  code. When you change them, a maintainer applies them with
  `gh api -X PUT repos/rossedman/guardrails/rulesets/<id> --input .github/rulesets/<file>.json`.
  Add new CI jobs that run on every pull request to the required checks in
  `main.json`.
- Questions and ideas go in a
  [GitHub issue](https://github.com/rossedman/guardrails/issues/new/choose).

## Commit messages

Use [Conventional Commits](https://www.conventionalcommits.org/):
`feat:`, `fix:`, `docs:`, `test:`, `build:`, `ci:`, `chore:`, for example
`fix(go): test packages that depend on the change`.
Release notes are generated from them.

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
