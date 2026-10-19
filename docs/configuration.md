# Configuration

Most repositories need two lines. Pick a [preset](presets.md) — **strict**
(the default), **standard** or **adopt** — and override only what you want
different:

```yaml
# .guardrails.yaml
version: 1
preset: adopt
```

`/guardrails:setup` (or `guardrails init --preset adopt`) writes this file
for you. `guardrails init --full` writes every setting instead, with its
default and a comment.

Configuration is layered. Each layer overrides the previous one key by key:

1. Built-in defaults.
2. The [preset](presets.md) (`preset:` in either file below; the
   repository's wins).
3. The global file, `~/.config/guardrails/config.yaml` (or
   `$XDG_CONFIG_HOME/guardrails/config.yaml`).
4. The repository's `.guardrails.yaml`.

With no files at all, the strict preset applies and every module whose files
exist is on. Lists replace rather than merge.

**Unknown keys are an error.** The loader is strict, so a typo can never
silently disable a check. Point your editor at the
[JSON Schema](https://raw.githubusercontent.com/rossedman/guardrails/main/internal/config/schema.json)
for completion and validation (the template from `guardrails init` already
does), or print it with `guardrails schema`. `guardrails config` prints the
effective, merged configuration.

`.guardrails.yaml` is protected: Claude cannot edit it. It proposes changes
in chat and you apply them.

## Reference

Every key, with the strict preset's values:

```yaml
version: 1
preset: strict           # strict | standard | adopt
mode: enforce            # enforce | warn (warn = never block, report only)

go:
  enabled: true          # every module defaults to on when its files exist
  coverage:
    total: 90            # min statement coverage of touched packages
    per_package_floor: 80
    ratchet: true        # never below the SessionStart baseline
    exclude: ["**/mocks/**", "**/*_mock.go", "**/*.pb.go", "cmd/**/main.go"]
  patch_coverage: 90     # min coverage of lines changed this session; 0 disables
  lint:
    config: .golangci.yml  # falls back to the bundled config
    on_edit: fast          # fast | full | off
  race: true
  vuln: true
  # Used by `guardrails harden` (on demand, not the gate):
  mutation: { tool: builtin, min_score: 70, budget_seconds: 120 }
  bench:
    packages: []         # performance-critical packages, e.g. internal/codec/...
  fuzz: { fuzztime: 10s, budget_seconds: 60 }

python:
  coverage: { total: 90, ratchet: true }
  patch_coverage: 90
  typecheck: mypy        # mypy | pyright | off
  strict: true           # mypy --strict

shell:
  severity: style        # minimum shellcheck severity: style | info | warning | error

yaml:
  actionlint: true       # GitHub Actions workflows
  kubeconform:
    enabled: true        # Kubernetes manifests (apiVersion + kind)
    kubernetes_version: "1.31"

terraform:
  binary: terraform      # terraform | tofu
  tflint: true
  scan: trivy            # trivy | off
  require_version_constraints: true

docker:
  require_digest_pins: true
  require_non_root: true
  build_check: true      # docker build --check, when a daemon is available

tools:
  install: auto          # auto | never
  prefer: path           # path | managed

guards:
  allow_suppression_tag: "guardrails:allow"
  kubectl_safe_contexts: ["kind-*", "*-dev"]
  protected_branches: ["main", "master"]

gate:
  max_consecutive_blocks: 3   # then hand off to the human
  timeout_seconds: 90
```

## Recipes

=== "Legacy repo"

    Keep the ratchet, start with a lower bar, and raise it as debt is paid
    down:

    ```yaml
    version: 1
    go:
      coverage:
        total: 60
        per_package_floor: 40
    ```

=== "Trial run"

    See what Guardrails would block without blocking anything:

    ```yaml
    version: 1
    mode: warn
    ```

=== "Strict library"

    ```yaml
    version: 1
    go:
      coverage:
        total: 95
        per_package_floor: 90
      patch_coverage: 100
      lint:
        on_edit: full
    ```

=== "Match CI exactly"

    Ignore whatever is on each machine's `PATH` and always use the pinned
    tool versions:

    ```yaml
    version: 1
    tools:
      prefer: managed
    ```

=== "Personal defaults"

    `~/.config/guardrails/config.yaml` applies to every repo:

    ```yaml
    version: 1
    guards:
      kubectl_safe_contexts: ["kind-*", "*-dev", "minikube", "docker-desktop"]
    ```
