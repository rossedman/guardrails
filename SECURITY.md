# Security policy

Guardrails runs inside developer environments and makes allow/deny decisions
for an AI agent, so we take bypasses seriously.

## Supported versions

Only the latest release receives fixes while the project is pre-1.0.

## Reporting a vulnerability

Please **do not** open a public issue. Report privately through
[GitHub security advisories](https://github.com/rossedman/guardrails/security/advisories/new).
Include steps to reproduce and the impact you observed.

You can expect an acknowledgement within 3 business days and a fix or
mitigation plan within two weeks for confirmed issues. Reporters are
credited in the published advisory unless they ask not to be.

## Verifying releases

Every release's `checksums.txt` is signed keylessly with
[cosign](https://docs.sigstore.dev/) by the release workflow, and every
archive has a GitHub build provenance attestation. To check a download,
fetch the archive, `checksums.txt` and `checksums.txt.sigstore.json` from the
release, then:

```bash
cosign verify-blob checksums.txt \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp '^https://github.com/rossedman/guardrails/.github/workflows/release.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
sha256sum --ignore-missing -c checksums.txt
gh attestation verify guardrails_*.tar.gz --repo rossedman/guardrails
```

## In scope

- A command or edit that should be denied by a [policy
  guard](https://rossedman.io/guardrails/guards/) but is allowed
  (for example, a shell construct that hides `terraform apply` or a write to
  `.guardrails.yaml`).
- A way for Claude to make the Stop gate pass without meeting the configured
  standards.
- Issues in the plugin launcher's download and checksum verification.

## Out of scope

Guardrails is defense in depth, not a sandbox. Claude Code's permission
system (`permissions.deny`) remains the primary control for hard denies;
see the docs for the recommended rules.
