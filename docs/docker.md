# Docker module

The Docker module owns `Dockerfile`, `Dockerfile.*`, `*.Dockerfile`,
`*.dockerfile`, `Containerfile` and `Containerfile.*`.

It needs **no linter installed**: Guardrails parses Dockerfiles itself and
applies a small set of native, high-signal rules. The only external tool is
the optional `docker` CLI, used for `docker build --check` when a daemon is
running.

| Stage | What runs |
|---|---|
| **On edit: auto-fix** | Nothing; Dockerfiles have no standard formatter. |
| **On edit: check** | The native rules below, on the edited file. Pure Go, a few milliseconds. |
| **At Stop: gate** | The native rules over every touched Dockerfile, then `docker build --check -f <file> <dir>` when the docker CLI is installed and `docker info` succeeds. Otherwise the gate notes that the build check was skipped. |

## Rules

| ID | Check | Flags | Fix |
|---|---|---|---|
| `GR-DOCKER-001` | `digest-pins` | A `FROM` image not pinned by `@sha256:`. `scratch` and earlier stage names are exempt; global `ARG` defaults are expanded; an `ARG` without a default can't be verified and is flagged. | Pin with `image:tag@sha256:<digest>` (`docker buildx imagetools inspect <image:tag>`) |
| `GR-DOCKER-002` | `non-root` | A final stage whose last `USER` is `root`, `0` or `0:<gid>`, or that sets no `USER` at all. A final stage built `FROM` an earlier stage inherits that stage's `USER`; base images named `nonroot` or `rootless` (e.g. distroless `:nonroot`) are trusted. | Add a non-root `USER` in the final stage |
| `GR-DOCKER-003` | `image-tag` | A `FROM` image with no tag or `:latest`. Only when `require_digest_pins` is off, since `GR-DOCKER-001` already covers it. | Use an explicit version tag |
| `GR-DOCKER-004` | `add-vs-copy` | `ADD` of local files. URLs, git sources and tar archives (which `ADD` extracts) are fine. | Use `COPY` |
| `GR-DOCKER-005` | `apt-recommends` | `apt-get install` / `apt install` without `--no-install-recommends`. | Add `--no-install-recommends` |
| `GR-DOCKER-006` | `apt-cleanup` | `apt-get install` in a `RUN` that doesn't remove `/var/lib/apt/lists` and doesn't use a cache mount. | End the `RUN` with `&& rm -rf /var/lib/apt/lists/*` |
| `GR-DOCKER-007` | `apk-cache` | `apk add` without `--no-cache` or a cache mount. | Use `apk add --no-cache` |
| `GR-DOCKER-008` | `build-secrets` | `ARG` or `ENV` names that look like secrets (`*TOKEN*`, `*PASSWORD*`, `*SECRET*`, `*API_KEY*`, `*PRIVATE_KEY*`, `*ACCESS_KEY*`, `*CREDENTIALS*`). Names ending in `_FILE`, `_PATH` or `_DIR` are fine. | Use `RUN --mount=type=secret` |

`docker build --check` adds BuildKit's own [build
checks](https://docs.docker.com/reference/build-checks/) (`FromAsCasing`,
`LegacyKeyValueFormat`, `JSONArgsRecommended`, …), reported with their rule
names.

## Parser

The parser handles parser directives (`# escape=`), comments (including
comment lines inside continuations), line continuations, case-insensitive
instructions, `--platform` flags, `AS` aliases, global `ARG` defaults with
`$VAR`, `${VAR}`, `${VAR:-default}` and `${VAR:+alt}`, and — best effort —
heredocs on `RUN`, `COPY` and `ADD`, whose bodies are skipped so their
contents are never mistaken for instructions. It is fuzz-tested.

## Configuration

```yaml
docker:
  enabled: true            # default: on when Dockerfiles exist
  require_digest_pins: true
  require_non_root: true
  build_check: true        # docker build --check when a daemon is available
```

## Toolchain

| Tool | Required | Missing means |
|---|---|---|
| `docker` CLI and daemon | no | `docker build --check` is skipped with a note to you |
