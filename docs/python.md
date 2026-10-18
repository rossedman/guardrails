# Python module

The Python module owns `*.py` files and `pyproject.toml`.

| Stage | What runs |
|---|---|
| **On edit: auto-fix** | `ruff format`, then `ruff check --fix`, using the project's ruff config. |
| **On edit: check** | `ruff check` and the type checker (`mypy`, `--strict` by default, or `pyright`) on the edited file, reporting issues in that file. |
| **At Stop: gate** | `ruff check` on touched files, `pytest` with coverage in the project's environment, the type checker on touched packages, and `uv lock --check` when `uv.lock` exists. |

Files are grouped by their nearest `pyproject.toml`, `setup.py` or
`setup.cfg`, so monorepos with several Python projects are gated project by
project. Without any of those, the repository root is the project.

## Running tests in the project's environment

pytest must see the project's own dependencies, so Guardrails runs it in the
project's environment, never in an isolated tool venv. The first match wins:

| Project has | pytest runs as |
|---|---|
| `uv.lock` | `uv run --frozen --with pytest --with pytest-cov pytest …` |
| `.venv/` | `uv run --no-project --python .venv/bin/python --with pytest --with pytest-cov pytest …` |
| `pyproject.toml` with `[project]` | `uv run --no-project --with-editable . --with pytest --with pytest-cov pytest …` |
| nothing else | `uv run --no-project --with pytest --with pytest-cov pytest …` |
| `.venv/` but no uv available | `.venv/bin/python -m pytest …` (pytest-cov must be installed there) |

`--with` layers pytest and pytest-cov onto the environment without changing
`pyproject.toml` or `uv.lock`. Coverage's data file, mypy's cache and
pytest's cache are kept out of the project.

## Coverage rules

**Total.** Statement coverage reported by coverage.py must be at least
`python.coverage.total` (default 90%). Exclusions come from the project's
own `[tool.coverage]` config, which Claude cannot edit.

**Ratchet.** With `python.coverage.ratchet: true` (the default),
no package directory may end below its coverage at SessionStart. Python
packages are tracked as `python:<dir>` in the baseline, separately from Go.

**Patch coverage.** Lines added or changed this session in non-test files
— `git diff` against the commit at SessionStart, plus untracked files —
must be at least `python.patch_coverage` covered (default 90%). Lines
coverage.py doesn't count as statements are ignored. Failures list the
uncovered changed lines per file:

```text
[patch-coverage] 66.7% of changed lines covered < 90.0% (4/6)
  pkg/client.py:41-42
  fix: test the changed lines above; they are code you wrote this session
```

On failure, the coverage report lists the five files with the most missing
statements and their missing line ranges.

## Type checking

`python.typecheck` picks `mypy` (default), `pyright`, or `off`.

- **mypy** is provisioned automatically. When the project has a `.venv`,
  mypy runs with `--python-executable .venv/bin/python` so third-party
  imports resolve. `python.strict: true` adds `--strict`.
- **pyright** must be on your `PATH` (`npm install -g pyright` or
  `pipx install pyright`); it is not provisioned.

Per edit, only issues in the edited file are reported. At the gate, the
touched packages are checked and issues in touched files are reported, so
existing debt elsewhere never blocks.

## Toolchain

Tools are resolved in this order: the project's `.venv/bin/<tool>`, then your
`PATH` (unless `tools.prefer: managed`), then a pinned, checksum-verified
copy Guardrails installs itself.

| Tool | Provisioned | Missing means |
|---|---|---|
| `ruff` | yes | The gate fails with a `tools` check; edits aren't formatted |
| `mypy` | yes | The gate fails with a `tools` check |
| `uv` | yes | Tests fall back to `.venv/bin/python -m pytest`; without a `.venv` the gate fails |
| `pyright` | no | Only needed with `typecheck: pyright` |

Python itself doesn't need to be installed: uv provides an interpreter when
the host has none.
