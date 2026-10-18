# YAML module

The YAML module owns `*.yml` and `*.yaml` files. YAML has no safe
auto-fixer, so the same checks run when Claude edits a file and, over every
YAML file touched this session, at the Stop gate.

| Check | Runs on | What it catches |
|---|---|---|
| **yamllint** | Every YAML file | Syntax errors, duplicate keys, bad indentation. Only `[error]` findings block; warnings are ignored. |
| **actionlint** | `.github/workflows/*.yml` | Invalid workflow syntax, unknown expressions and contexts, bad `needs`, shellcheck issues in `run:` steps. |
| **kubeconform** | Files with a document that has top-level `apiVersion` and `kind` | Kubernetes manifests that don't match the API schema, in strict mode (unknown fields fail). |

## yamllint config

If the repository has a `.yamllint`, `.yamllint.yaml` or `.yamllint.yml`, it
is used as-is. Otherwise Guardrails runs yamllint's `relaxed` preset, which
reports real errors (duplicate keys, syntax) without opinions on line
length or document markers.

## GitHub Actions

Workflows directly under `.github/workflows/` are checked with actionlint,
run from the repository root so it picks up `.github/actionlint.yaml`.
Turn it off with `yaml.actionlint: false`.

## Kubernetes manifests

Any document in a multi-document file with top-level `apiVersion` and `kind`
makes the file a manifest. kubeconform validates it in `-strict` mode
against the schemas for `yaml.kubeconform.kubernetes_version` (default
`1.31`; `master` is also accepted).

- **Custom resources** without a published schema are skipped
  (`-ignore-missing-schemas`), not failed.
- **Helm templates** (files under a `templates/` directory containing `{{`)
  are skipped: they aren't YAML until rendered.
- **Offline**: schemas are downloaded on first use and cached in the plugin
  data directory. If a download fails, validation is skipped with a note to
  you instead of blocking Claude.

Turn it off with `yaml.kubeconform.enabled: false`.

## Configuration

```yaml
yaml:
  enabled: true          # default: on when YAML files exist
  actionlint: true
  kubeconform:
    enabled: true
    kubernetes_version: "1.31"
```

## Toolchain

| Tool | Required | Provisioned as |
|---|---|---|
| `yamllint` | yes | Python tool in an isolated, hash-locked uv venv |
| `actionlint` | when `yaml.actionlint` is on and a workflow is touched | Release binary, checksum-verified |
| `kubeconform` | when `yaml.kubeconform.enabled` is on and a manifest is touched | Release binary, checksum-verified |

Tools on your `PATH` are used first (unless `tools.prefer: managed`). On
edit, a tool that is still installing is skipped. At the gate, a required
tool that can't be resolved fails a `tools` check, so nothing passes
unchecked.
