# Terraform module

The Terraform module owns `*.tf` and `*.tfvars` files (not `*.tf.json`, and
nothing under `.terraform/`). A Terraform *module* is the directory holding
the file.

| Stage | What runs |
|---|---|
| **On edit: auto-fix** | `terraform fmt` on the edited file. |
| **On edit: check** | `tflint` on the edited file's module, reporting issues in that file. Plugins are never installed at edit time; if tflint needs `--init`, the check waits for the gate. |
| **At Stop: gate** | Per touched module: `tflint --init` + tflint, `terraform init -backend=false` + `validate`, a trivy config scan, and provider version constraints. |

Set `terraform.binary: tofu` to use OpenTofu instead; every command above
runs with `tofu`.

## Plans only

The gate never touches real infrastructure or state. The
[policy guards](guards.md#blast-radius) deny `apply`, `destroy`, `import`,
`state rm/mv` and `-auto-approve`, so Claude can write and validate
Terraform and hand you a plan, but applying it is yours.

## Nothing written to your repo

Every `terraform` command runs with `TF_DATA_DIR` and `TF_PLUGIN_CACHE_DIR`
under the plugin data directory, so the gate never creates `.terraform/` in
the module, and providers are downloaded once and shared across modules.

- When `.terraform.lock.hcl` exists, init runs with `-lockfile=readonly`.
  The lockfile guard forbids editing it, and the gate must not change it
  either.
- When there is no lock file, the one init writes is removed again.

## Checks

**validate.** `terraform validate -json` errors, with `file:line`.
Warnings are not reported.

**tflint.** Errors and warnings from tflint, honoring the repo's
`.tflint.hcl`. Notices are dropped. `# tflint-ignore` comments are denied by
the suppression guard.

**trivy.** `trivy config` misconfigurations of severity HIGH or CRITICAL,
reported with their check ID (for example `AVD-AWS-0086`). Set
`terraform.scan: off` to skip it.

**versions.** With `terraform.require_version_constraints: true` (the
default), every provider in `required_providers` must have a `version`
constraint. Providers used by `provider`, `resource`, `data` or `ephemeral`
blocks with no `required_providers` entry at all are flagged too. The
built-in `terraform` provider is exempt.

Issues are scoped like the Go module: tflint, trivy and version findings
are reported only in files Claude touched (or everywhere with
`guardrails gate --all`). Validation errors are reported for the whole
module, because a change in one file can break another.

## Network failures

`init` needs registry access for providers and modules. When init fails
because a registry can't be reached, the gate tells you in `systemMessage`
and skips validate instead of blocking Claude on something it can't fix.
Configuration errors during init (an unsupported block, a bad module
source) still block. A failed `tflint --init` or trivy run is a note too.

## Toolchain

| Tool | Required | Provisioned |
|---|---|---|
| `terraform` or `tofu` | yes | managed, pinned and checksum-verified |
| `tflint` | when `terraform.tflint: true` | managed |
| `trivy` | when `terraform.scan: trivy` | managed |

A required tool that is still unavailable at Stop fails the gate with a
`tools` check, so the gate never passes silently.

## Configuration

```yaml
terraform:
  binary: terraform                  # terraform | tofu
  tflint: true
  scan: trivy                        # trivy | off
  require_version_constraints: true
```
