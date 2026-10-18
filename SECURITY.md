# Security policy

Guardrails runs inside developer environments and makes allow/deny decisions
for an AI agent, so we take bypasses seriously.

## Supported versions

Only the latest release receives fixes while the project is pre-1.0.

## Reporting a vulnerability

Please **do not** open a public issue. Report privately through
[GitHub security advisories](https://github.com/rossedman/guardrails/security/advisories/new).
Include steps to reproduce and the impact you observed.

You can expect an acknowledgement within a few days and a fix or mitigation
plan within two weeks for confirmed issues.

## In scope

- A command or edit that should be denied by a [policy
  guard](https://rossedman.github.io/guardrails/guards/) but is allowed
  (for example, a shell construct that hides `terraform apply` or a write to
  `.guardrails.yaml`).
- A way for Claude to make the Stop gate pass without meeting the configured
  standards.
- Issues in the plugin launcher's download and checksum verification.

## Out of scope

Guardrails is defense in depth, not a sandbox. Claude Code's permission
system (`permissions.deny`) remains the primary control for hard denies;
see the docs for the recommended rules.
