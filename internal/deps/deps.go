// Package deps checks the packages Claude is about to add before it adds
// them.
//
// A PreToolUse hook hands it either a Bash command line (npm install, pip
// install, go get, cargo add and their relatives, including package
// runners such as npx and uvx that execute a package outright) or a
// manifest edit (package.json, requirements.txt, pyproject.toml, go.mod,
// Cargo.toml). FromCommand and FromManifest turn those into Requests, and
// Checker looks each one up on deps.dev and OSV:
//
//   - a package that doesn't exist is denied: agents invent plausible names,
//     and attackers register them ("slopsquatting");
//   - a package flagged as malicious, or under a denied license, is denied;
//   - a near miss of a popular name (a typosquat), a package or version
//     younger than the configured minimum, a deprecated or unknown version,
//     a license outside the allow list and known advisories ask first.
//
// Lookups run concurrently within a time budget and are cached for a day.
// When the registries can't be reached, deps.unreachable decides. Only
// package names and versions leave the machine.
package deps

import (
	"regexp"
	"strings"
)

//go:generate go run ./popular/gen

// Package systems.
const (
	NPM   = "npm"
	PyPI  = "pypi"
	Go    = "go"
	Cargo = "cargo"
)

// Request is one package Claude is about to add.
type Request struct {
	// System is npm, pypi, go or cargo.
	System string
	// Name is normalized as the registry expects: PEP 503 for PyPI, the
	// module or package path for Go.
	Name string
	// Version is the requested version or range; "" means the latest.
	Version string
	// Source is what asked for it, such as "npm install lodash" or
	// "package.json".
	Source string
	// Private is set when a custom registry or index was named, so the
	// package may legitimately be missing from the public registry.
	Private bool
}

// String renders the request as name or name@version.
func (r Request) String() string {
	if r.Version == "" {
		return r.Name
	}
	return r.Name + "@" + r.Version
}

var pep503 = regexp.MustCompile(`[-_.]+`)

// NormalizePyPI normalizes a PyPI project name per PEP 503.
func NormalizePyPI(name string) string {
	return pep503.ReplaceAllString(strings.ToLower(name), "-")
}

// registryName is the public registry a system's packages live in.
func registryName(system string) string {
	switch system {
	case NPM:
		return "npm"
	case PyPI:
		return "PyPI"
	case Go:
		return "the Go module proxy"
	default:
		return "crates.io"
	}
}
