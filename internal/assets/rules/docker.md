---
paths:
  - "**/Dockerfile"
  - "**/Dockerfile.*"
  - "**/*.Dockerfile"
  - "**/*.dockerfile"
  - "**/Containerfile*"
---

# Dockerfile standards (enforced by Guardrails)

Guardrails checks every Dockerfile you edit with its own rules (GR-DOCKER-*)
and blocks the end of your turn until they pass. When a Docker daemon is
available it also runs `docker build --check`. Meet the bar up front.

## Base images

- Pin every external base image by digest: `FROM golang:1.26@sha256:<digest>`.
  Get the digest with `docker buildx imagetools inspect golang:1.26`. Keep the
  tag next to the digest so humans can read it.
- Never use `:latest` or an untagged image.
- Prefer small, maintained bases (distroless, alpine, `-slim`).

## Build structure

- Use multi-stage builds: compile in a builder stage, copy only the artifact
  into a minimal final stage (`FROM scratch` or distroless when possible).
- Order instructions from least to most frequently changing so layer caching
  works; copy dependency manifests and install dependencies before copying
  source.
- Use `COPY`, not `ADD`, for local files. `ADD` is only for remote URLs or
  tar archives you want extracted.
- Combine package installation and cleanup in one `RUN`:
  `apt-get update && apt-get install -y --no-install-recommends pkg && rm -rf /var/lib/apt/lists/*`,
  or `apk add --no-cache pkg`. Pin package versions where practical.
- Prefer the exec form for `CMD` and `ENTRYPOINT`: `CMD ["/app", "serve"]`.

## Runtime

- The final stage must run as a non-root user: create one if needed and set
  `USER app` (or a numeric UID such as `65532`). Never end on `USER root`.
- Add a `HEALTHCHECK` for long-running services when the orchestrator
  doesn't provide one.
- Expose only the ports the service listens on.

## Secrets

- Never put secrets in `ARG` or `ENV`; both persist in the image history.
  Use BuildKit secret mounts: `RUN --mount=type=secret,id=npm_token ...`.
- Never `COPY` credential files (`.npmrc`, `.env`, keys) into the image;
  keep them out with `.dockerignore`.

## Never

- `# hadolint ignore` or other suppressions, `docker push`, or editing
  generated files. Propose exceptions to the human instead.
