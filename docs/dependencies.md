# Dependency check

Coding agents add packages casually, and that's a supply-chain risk. They
invent plausible package names that don't exist, and attackers register
those names ("slopsquatting"). They mistype popular ones. They pull in
packages published yesterday. Guardrails looks up every package Claude is
about to add, **before** the command runs or the manifest changes.

## What it catches

| Finding | Example | Decision |
|---|---|---|
| The package doesn't exist | `pip install reqeusts`: *"reqeusts" doesn't exist on PyPI — did you mean "requests"?* | deny |
| A known malicious package (OSV `MAL-` advisory) | `npm install lodahs`: *flagged as malicious — it imitates "lodash"* | deny |
| A license in `deps.licenses.deny` | an AGPL dependency in a proprietary repo | deny |
| A near miss of a popular name (typosquat) | `crossenv` for `cross-env`, `reactdom` for `react-dom` | ask |
| A package first published recently | younger than `deps.min_age_days` (30) | ask |
| A version published recently | younger than `deps.min_version_age_days` (off by default) | ask |
| A deprecated version, or a pinned version that doesn't exist | `lodash@4.99.0` | ask |
| A license outside `deps.licenses.allow` | when an allow list is set | ask |
| Known vulnerabilities in the version | `requests==2.19.0` | `deps.vulnerabilities` (ask) |

Typosquats are spotted against the 5,000 most-downloaded npm and PyPI
packages. A name counts as a near miss when it differs from one of them by
a single letter added, dropped, changed or swapped, or by case and
separators alone. Popular packages themselves never trigger it.

## Where it hooks

The check runs in **PreToolUse**, so nothing is installed until it passes.

**Commands Claude runs in Bash:**

| Ecosystem | Commands |
|---|---|
| npm | `npm install`/`i`/`add`, `pnpm add`, `yarn add`, `bun add`, and runners that execute a package: `npx`, `npm exec`, `pnpm dlx`, `pnpx`, `yarn dlx`, `bunx` |
| PyPI | `pip install`, `python -m pip install`, `uv add`, `uv pip install`, `poetry add`, and runners: `uvx`, `uv tool install`/`run`, `pipx install`/`run` |
| Go | `go get`, `go install` |
| Rust | `cargo add`, `cargo install` |

**Manifest edits.** Dependencies added to, or re-versioned in, any of these
files are checked too:

- `package.json`
- `requirements*.txt` and `*.in`
- `pyproject.toml` (PEP 621, dependency groups, Poetry)
- `go.mod`
- `Cargo.toml`

The check skips sources that aren't registry packages: local paths, URLs,
git sources, tarballs, wheels and requirement files.

## Configuration

```yaml
deps:
  enabled: true
  min_age_days: 30          # ask before a package younger than this; 0 = off
  min_version_age_days: 0   # ask before a version younger than this; 0 = off
  vulnerabilities: ask      # ask | deny | allow, for versions with advisories
  unreachable: ask          # ask | allow, when the lookups can't finish
  licenses:
    allow: []               # SPDX ids; empty allows any license not denied
    deny: []                # e.g. [AGPL-3.0-only, SSPL-1.0]
  allow: []                 # packages that skip the check, e.g. ["@acme/*"]
```

Licenses are compared case-insensitively.

- **`OR` expressions:** `MIT OR Apache-2.0` passes if either license is
  allowed. It's denied only when every alternative is denied.
- **Multiple licenses:** a package that lists several licenses needs all of
  them to pass.

`min_version_age_days` catches hijacked releases: a popular package's new
version, published from a stolen maintainer account, is usually caught and
pulled within days. Setting it to 2–7 makes Claude wait those days out.

## Private registries

Packages from a private registry aren't on the public ones. The check
treats them as follows:

- **A custom registry or index on the command line** (`--registry`, `-i`,
  `--index-url`, `--extra-index-url`, `--index`, `cargo add --registry`): a
  package missing from the public registry is allowed. If the package
  *does* exist publicly, it's still checked. That's the classic
  dependency-confusion attack.
- **Go modules matching `GOPRIVATE`, `GONOSUMDB` or `GONOPROXY`** are
  treated the same way.
- **Scoped registries set in `.npmrc`** can't be seen from the command
  line. List those scopes in `deps.allow`, for example `["@acme/*"]`.

## Privacy and speed

Lookups go to [deps.dev](https://deps.dev) for package metadata and to
[OSV](https://osv.dev) for malicious-package reports. Only package names and
versions are sent.

Each command's lookups run concurrently within a 4-second budget, and
results are cached for a day in the data dir. When the budget runs out or
the services are down, `deps.unreachable` decides: by default Claude asks
you.
