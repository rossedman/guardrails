---
paths:
  - "**/*.tf"
  - "**/*.tfvars"
---

# Terraform standards (enforced by Guardrails)

Guardrails runs `terraform fmt` on every Terraform file you edit and tflint
on its module. Before your turn can end, touched modules must pass `init
-backend=false` and `validate`, be tflint clean, have no HIGH or CRITICAL
trivy findings, and pin every provider version.

## Code

- Declare every provider in `terraform { required_providers { ... } }` with
  `source` and a `version` constraint (`~> 5.0`), and set `required_version`.
- Type every variable and give it a `description`. Add `validation` blocks
  for values with a known shape.
- Prefer `for_each` over `count` for anything that has identity.
- Keep secrets out of `.tf` and `.tfvars`. Read them from a secret store,
  or mark variables and outputs `sensitive = true`.
- Encrypt and block public access by default (S3, storage accounts, disks,
  databases), and scope IAM to least privilege.
- One concern per module: `variables.tf`, `outputs.tf`, `main.tf`,
  `versions.tf`.

## Plans, never applies

- Run `terraform plan` to check your work and summarize it for the human.
- Never run `apply`, `destroy`, `import`, `state` subcommands or
  `-auto-approve`. These are denied, and they are the human's to run.

## Never

- `# tflint-ignore` or trivy ignore comments, editing `.terraform.lock.hcl`
  by hand (run `terraform init -upgrade` and let the human review), or
  touching `.guardrails.yaml`. Propose changes to the human instead.
