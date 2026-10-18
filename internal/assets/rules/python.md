---
paths:
  - "**/*.py"
  - "**/pyproject.toml"
---

# Python standards (enforced by Guardrails)

Guardrails formats every Python file you edit (ruff format, ruff --fix),
checks it with ruff and the type checker, and blocks the end of your turn
until pytest passes, coverage meets the bar, and touched files are ruff and
type-check clean. Meet the bar up front instead of on request.

## Code

- Type every function signature, including return types. Code must pass
  `mypy --strict`: no implicit `Any`, no untyped defs.
- Raise specific exceptions with context; chain with `raise ... from err`.
  Never use a bare `except:` or swallow exceptions silently.
- Prefer pure functions and explicit dependencies over module-level state.
  Pass clocks, clients and paths in rather than reaching for globals.
- Use `pathlib.Path`, f-strings, dataclasses and context managers.
- Keep public functions and classes documented with a one-line docstring.

## Tests

- Write pytest tests in the same change as the code, next to it as
  `test_*.py`. Use `@pytest.mark.parametrize` for table-driven cases.
- Assert behavior: every test checks return values, raised exceptions
  (`pytest.raises`) or side effects. Coverage that only runs code is not
  enough.
- Use fixtures such as `tmp_path`, `monkeypatch` and `capsys` instead of
  touching real files, environment or network.
- Cover error paths. The gate lists files with missing lines, and lines you
  changed this session must be covered too.

## Never

- `# noqa`, `# type: ignore`, `# pragma: no cover`, `@pytest.mark.skip`, or
  an `xfail` without a linked issue. These are denied.
- Editing `[tool.ruff]`, `[tool.mypy]`, `[tool.coverage]` or `[tool.pytest]`
  in `pyproject.toml`, or `uv.lock` by hand. Propose changes to the human;
  run `uv lock` to update the lockfile.
