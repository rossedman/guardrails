# Policy guards

PreToolUse guards come in two kinds: **anti-gaming** guards stop Claude
from weakening the standards, and **blast-radius** guards keep irreversible
operations with you. Guards always apply, including in `mode: warn`.

## Anti-gaming

| Guard | Trigger | Decision |
|---|---|---|
| `protected-config` | Edit/Write to `.guardrails.yaml`, `.golangci.{yml,yaml,toml,json}`, `.claude/settings*.json`, `.claude/rules/*.md`, or changes to `[tool.ruff]`, `[tool.mypy]`, `[tool.coverage]`, `[tool.pytest]` in `pyproject.toml`. Also shell writes to those files (`>`, `>>`, `tee`, `sed -i`, `perl -pi`, `rm`, `mv`, `cp`, …). | deny |
| `suppression` | New lines adding `//nolint`, `# noqa`, `# type: ignore`, `# pragma: no cover`, `//go:build ignore`, `# tflint-ignore`, `# hadolint ignore`, `# shellcheck disable`, `//lint:ignore` | deny, unless the line directly above is a `guardrails:allow <reason>` comment **you** wrote |
| `skipped-test` | New `t.Skip`/`t.Skipf`/`t.SkipNow`, `@pytest.mark.skip`/`skipif`, `pytest.skip(`, `@unittest.skip` | deny, unless guarded by `testing.Short()` |
| `skipped-test` | New `xfail` without a linked issue (URL to an issue/PR, or a `PROJ-123` key) | deny |
| `lowered-threshold` | An edit that lowers or removes `--cov-fail-under` / `fail_under`, or a Bash command passing `--cov-fail-under` below the configured threshold | deny |
| `generated-file` | Edit to a file with a `Code generated ... DO NOT EDIT` header | deny |
| `lockfile` | Direct edit of `go.sum`, `uv.lock`, `poetry.lock`, `.terraform.lock.hcl` | deny |
| `secrets` | New content matching high-confidence secret patterns (AWS, GitHub, Slack, Stripe, Google, Anthropic, OpenAI keys; private key blocks), or any write to `.env*` (except `.env.example`, `.sample`, `.template`) | deny |
| `test-writer` | The `test-writer` subagent editing anything but test files | deny |

The suppression, skipped-test, secret and threshold guards compare the file
before and after the edit, so they only fire on **new** lines — never on
ones already in the file.

### Allowing a suppression

When a suppression really is right, write the tag yourself, on the line
above:

```go
// guardrails:allow gosec G404 is fine here, this RNG seeds test fixtures
rng := rand.New(rand.NewSource(seed)) //nolint:gosec
```

Claude adding a `guardrails:allow` tag is itself denied, so the escape hatch
stays yours.

## Blast radius

| Guard | Trigger | Decision |
|---|---|---|
| `terraform-state` | `terraform`/`tofu`/`terragrunt` `apply`, `destroy`, `import`, `taint`, `untaint`, `force-unlock`, `state rm/mv/push/…`, `workspace delete`, or any `-auto-approve` | deny |
| `registry-push` | `docker`/`podman` `push` or `image push`, `buildx build --push`, `--output type=registry`, `crane`/`skopeo`/`oras`/`ko` push or copy | ask |
| `git-history` | `git push --force`/`-f`/`--force-with-lease`/`--mirror`/`--delete`, `+refspec`, `--no-verify` on push/commit/merge/rebase, `commit -n`, `reset --hard` | deny |
| `git-history` | Push to a protected branch (`main`, `master` by default), explicitly or implicitly from the current branch | deny |
| `git-history` | `git clean -f` | ask |
| `cluster-write` | `kubectl`/`oc` `apply`, `delete`, `edit`, `patch`, `replace`, `scale`, `create`, `drain`, `cordon`, `rollout restart`…, or `helm install/upgrade/uninstall/rollback`, against a context not matching `guards.kubectl_safe_contexts` (default `kind-*`, `*-dev`) | ask |

Bash commands are split on `;`, `&&`, `||`, `|`, `&`, newlines, subshells
and command substitution, and leading `sudo`, `env`, `VAR=value`, `command`,
`exec`, `time` and `nohup` are stripped, so `cd infra && sudo terraform
apply` is caught.

## Belt and braces

The hooks doc calls `if`-matching best effort, and hooks can be disabled, so
hard denies should also live in `permissions.deny`. Plugins can't ship
permission rules; `guardrails init` prints a ready-made block to merge into
`.claude/settings.json`.
