# Same gate everywhere

The checks Claude faces at Stop are the same binary and the same
`.guardrails.yaml` you can run in CI and before every commit. When all three
agree, "it passed the gate" means the same thing for Claude, for you, and
for the pull request.

| Where | Runs | Scope |
|---|---|---|
| Claude Code (plugin) | `guardrails hook Stop` | Files Claude touched this session |
| pre-commit | `guardrails gate --staged` | Files staged for the commit |
| GitHub Actions | `guardrails gate --base origin/<base>` | Files changed on the pull request |
| Anywhere | `guardrails gate --all` | Every package |

Tools are [provisioned the same way](tools.md) in every place: pinned,
checksum-verified, downloaded on first use and cached.

## GitHub Actions

```yaml
name: Guardrails

on:
  pull_request:
  push:
    branches: [main]

jobs:
  gate:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
        with:
          fetch-depth: 0          # --base needs the base branch's history
      - uses: actions/setup-go@v6 # the language runtimes your repo needs
        with:
          go-version-file: go.mod
      - uses: rossedman/guardrails@v0.4.0
```

On pull requests the action runs `gate --base origin/<base> --explain`:
only what the pull request changed is gated, and patch coverage is
measured against the base branch. On other events it runs
`gate --all --explain`. The full report is printed to the log and added to
the job summary; the job fails when the gate fails.

| Input | Default | Meaning |
|---|---|---|
| `version` | `latest` | Guardrails release to run, e.g. `0.4.0` |
| `args` | (see above) | Arguments for `guardrails`, e.g. `gate --all` or `harden --base origin/main` |
| `working-directory` | `.` | Where to run |
| `binary` | | A prebuilt `guardrails` to use instead of downloading one |
| `token` | `github.token` | Used to resolve `latest` |

The `passed` output is `"true"` when guardrails exited 0.

Pinned tools are cached between runs (`actions/cache`, keyed on the
Guardrails version and runner platform), so only the first run downloads
them. The action supports Linux and macOS runners on amd64 and arm64.

Running [`harden`](harden.md) on pull requests is a good fit for CI, where a
few minutes don't block anyone:

```yaml
      - uses: rossedman/guardrails@v0.4.0
        with:
          args: harden --base origin/${{ github.base_ref }}
```

## pre-commit

Add Guardrails to `.pre-commit-config.yaml`:

```yaml
repos:
  - repo: https://github.com/rossedman/guardrails
    rev: v0.4.0
    hooks:
      - id: guardrails
```

The hook runs `guardrails gate --staged`: only files staged for the commit
are gated, and patch coverage is measured against `HEAD`. Tests, coverage
and lint still run on the working tree, so unstaged edits in the same
packages are part of what gets checked — stash them if you want a commit
checked in isolation.

The hook uses the same launcher as the plugin: on first run it downloads
the release matching `rev`, verifies its checksum and caches it under
`$XDG_DATA_HOME/guardrails` (or `~/.local/share/guardrails`). Set
`GUARDRAILS_BIN` to use a binary you built yourself.

## Other CI systems

Anything that can run a shell command can run the gate. With Go available:

```bash
go install github.com/rossedman/guardrails/cmd/guardrails@v0.4.0
guardrails gate --base origin/main --explain    # changes on this branch
guardrails gate --all --explain                 # everything
```

For example, GitLab CI:

```yaml
guardrails:
  image: golang:1.26
  variables:
    GIT_DEPTH: 0
  script:
    - go install github.com/rossedman/guardrails/cmd/guardrails@v0.4.0
    - guardrails gate --base origin/$CI_MERGE_REQUEST_TARGET_BRANCH_NAME --explain
  rules:
    - if: $CI_PIPELINE_SOURCE == "merge_request_event"
```

Exit codes are the same everywhere: `0` when the gate passes, `1` when it
fails. `--json` prints machine-readable results.

## How results line up with Claude

The plugin, the hook and CI share everything that decides a result: the
binary version, `.guardrails.yaml`, the bundled lint config and the pinned
tool versions. Two differences are deliberate:

- **Scope.** Claude's Stop gate checks files Claude touched this session;
  pre-commit checks staged files; CI checks the pull request's changes. A
  file nobody touched never fails a gate it wasn't part of.
- **Retries.** In Claude Code the gate hands off to you after three
  consecutive failures. In CI and pre-commit a failure simply fails.

Use `tools.prefer: managed` in `.guardrails.yaml` to make every machine
use the pinned tool versions rather than whatever is on its `PATH`.
