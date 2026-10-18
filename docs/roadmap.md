# Roadmap

- [x] **v0.1 — Go.** PostToolUse fix and check, Stop gate with total,
  per-package, ratchet and patch coverage, config file with strict schema,
  feedback template, retry cap and hand-off, CLI (`gate`, `doctor`, `init`,
  `explain`, `statusline`).
- [x] **v0.2 — Guards.** Anti-gaming and blast-radius PreToolUse guards,
  plus a `permissions.deny` block from `guardrails init`. *(shipped early,
  alongside v0.1)*
- [ ] **v0.3 — Python, Shell, YAML.** ruff, mypy/pyright, pytest-cov, uv;
  shfmt and shellcheck; yamllint, actionlint, kubeconform.
- [ ] **v0.4 — Terraform, Docker.** `terraform fmt`, tflint, `init
  -backend=false` + `validate` with caching, trivy; hadolint, `docker build
  --check`, digest pins, non-root `USER`.
- [ ] **Test depth.** Mutation testing on changed functions (gremlins,
  mutmut), benchmarks with benchstat regression checks, fuzzing with crash
  capture, and a `go/analysis` pass to decide when each is warranted.
- [ ] **v0.5 — Companion tools.** `/guardrails:tf-review`,
  `/guardrails:insights` over `runs.jsonl` with drafted rule updates.
- [ ] **v1.0 — Same gate everywhere.** A pre-commit hook and a GitHub Action
  that call the identical binary and config.

## Open questions

- Should coverage count only touched packages, or the whole module once the
  repo is at 90%?
- mypy or pyright as the Python default?
- Is `docker build --check` enough, or should the Docker gate also verify
  SBOMs and signatures?
- Should `warn` mode be allowed per module, so a legacy repo can enforce Go
  but only warn on Terraform?

Have an opinion? [Open a discussion or issue](https://github.com/rossedman/guardrails/issues).
