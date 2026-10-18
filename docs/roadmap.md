# Roadmap

- [x] **v0.1 — Go.** PostToolUse fix and check, Stop gate with total,
  per-package, ratchet and patch coverage, config file with strict schema,
  feedback template, retry cap and hand-off, CLI (`gate`, `doctor`, `init`,
  `explain`, `statusline`).
- [x] **v0.1.x — Guards.** Anti-gaming and blast-radius PreToolUse guards,
  plus a `permissions.deny` block from `guardrails init`. *(shipped early,
  alongside v0.1)*
- [x] **v0.2 — Every language, no tool installs.** Python (ruff, mypy,
  pytest-cov, uv), Shell (shfmt, shellcheck), YAML (yamllint, actionlint,
  kubeconform), Terraform (fmt, tflint, validate, trivy, version
  constraints) and Docker (built-in Dockerfile checks, digest pins, non-root,
  `docker build --check`), with pinned, checksum-verified [tool provisioning](tools.md).
- [x] **Hardening, on demand.** `guardrails harden` and
  `/guardrails:harden`: built-in mutation testing of changed Go functions,
  short fuzz runs, and benchmark / fuzz-target suggestions; the Go rules
  teach the same criteria up front.
- [ ] **Python mutation testing** without writing into the project
  directory.
- [ ] **Benchmark regression checks** against the session's base commit.
- [x] **v0.4 — Dependents, insights, same gate everywhere.** The Go gate
  tests the packages that import changed code; `/guardrails:insights`
  drafts rule updates from `runs.jsonl`; a GitHub Action and a pre-commit
  hook run the identical binary and config.
- [x] **v0.5 — Supply chain and memory.** A dependency check on every
  install command and manifest edit, lessons captured from your corrections
  with `/guardrails:learn`, and a Cobra/Viper CLI.
- [ ] **Companion tools.** `/guardrails:tf-review`, `/guardrails:pr-ready`,
  `/guardrails:flaky`, `/guardrails:deps`.
- [ ] **TypeScript / JavaScript module.** eslint, prettier, tsc, vitest/jest.
- [ ] **Per-module warn mode**, test-result caching across edits, and shared
  org config.

## Open questions

- Should coverage count only touched packages, or the whole module once the
  repo is at 90%?
- mypy or pyright as the Python default?
- Is `docker build --check` enough, or should the Docker gate also verify
  SBOMs and signatures?
- Should `warn` mode be allowed per module, so a legacy repo can enforce Go
  but only warn on Terraform?

Have an opinion? [Open a discussion or issue](https://github.com/rossedman/guardrails/issues).
