package deps

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rossedman/guardrails/internal/guard"
)

// FromCommand returns the registry packages a Bash command line installs,
// adds or runs. Local paths, URLs, git sources and requirement files are
// skipped: they aren't registry lookups.
func FromCommand(command string) []Request {
	var out []Request
	for _, args := range guard.Commands(command) {
		src := strings.Join(args, " ")
		for _, r := range fromArgs(args) {
			r.Source = src
			out = append(out, r)
		}
	}
	return out
}

func fromArgs(args []string) []Request {
	bin := filepath.Base(args[0])
	rest := args[1:]
	sub := func(names ...string) ([]string, bool) {
		if len(rest) == 0 {
			return nil, false
		}
		for _, n := range names {
			if rest[0] == n {
				return rest[1:], true
			}
		}
		return nil, false
	}
	switch bin {
	case "npm":
		if a, ok := sub("install", "i", "in", "add", "isntall"); ok {
			return npmSpecs(a, false)
		}
		if a, ok := sub("exec", "x"); ok {
			return npmSpecs(a, true)
		}
	case "pnpm":
		if a, ok := sub("add", "install", "i"); ok {
			return npmSpecs(a, false)
		}
		if a, ok := sub("dlx"); ok {
			return npmSpecs(a, true)
		}
	case "yarn":
		if a, ok := sub("global"); ok {
			rest = a
		}
		if a, ok := sub("add"); ok {
			return npmSpecs(a, false)
		}
		if a, ok := sub("dlx"); ok {
			return npmSpecs(a, true)
		}
	case "bun":
		if a, ok := sub("add", "a", "install", "i"); ok {
			return npmSpecs(a, false)
		}
		if a, ok := sub("x"); ok {
			return npmSpecs(a, true)
		}
	case "npx", "pnpx", "bunx":
		return npmSpecs(rest, true)
	case "pip", "pip3":
		if a, ok := sub("install"); ok {
			return pipSpecs(a, false)
		}
	case "python", "python3", "py":
		if len(rest) >= 3 && rest[0] == "-m" && (rest[1] == "pip" || rest[1] == "pip3") && rest[2] == "install" {
			return pipSpecs(rest[3:], false)
		}
	case "uv":
		if a, ok := sub("pip"); ok && len(a) > 0 && a[0] == "install" {
			return pipSpecs(a[1:], false)
		}
		if a, ok := sub("add"); ok {
			return pipSpecs(a, false)
		}
		if a, ok := sub("tool"); ok && len(a) > 0 && (a[0] == "install" || a[0] == "run") {
			return pipSpecs(a[1:], true)
		}
	case "uvx":
		return pipSpecs(rest, true)
	case "poetry":
		if a, ok := sub("add"); ok {
			return pipSpecs(a, false)
		}
	case "pipx":
		if a, ok := sub("install", "run"); ok {
			return pipSpecs(a, true)
		}
	case "go":
		if a, ok := sub("get", "install"); ok {
			return goSpecs(a)
		}
	case "cargo":
		if a, ok := sub("add"); ok {
			return cargoSpecs(a, false)
		}
		if a, ok := sub("install"); ok {
			return cargoSpecs(a, true)
		}
	}
	return nil
}

// parsed is a command's positional arguments and the values of the flags
// that matter.
type parsed struct {
	positional []string
	values     map[string][]string
	bools      map[string]bool
}

// parseFlags splits args into flags and positionals. valueFlags take a
// value, as the next argument or after "="; every other flag is boolean.
// "--" ends the flags.
func parseFlags(args []string, valueFlags map[string]bool) parsed {
	p := parsed{values: map[string][]string{}, bools: map[string]bool{}}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			p.positional = append(p.positional, args[i+1:]...)
			return p
		case strings.HasPrefix(a, "-") && len(a) > 1:
			name, val, hasVal := strings.Cut(a, "=")
			switch {
			case hasVal:
				p.values[name] = append(p.values[name], val)
			case valueFlags[name] && i+1 < len(args):
				i++
				p.values[name] = append(p.values[name], args[i])
			default:
				p.bools[name] = true
			}
		default:
			p.positional = append(p.positional, a)
		}
	}
	return p
}

func (p parsed) has(names ...string) bool {
	for _, n := range names {
		if len(p.values[n]) > 0 || p.bools[n] {
			return true
		}
	}
	return false
}

func (p parsed) all(names ...string) []string {
	var out []string
	for _, n := range names {
		out = append(out, p.values[n]...)
	}
	return out
}

var npmValueFlags = set("--registry", "--tag", "-w", "--workspace", "--prefix", "--cache", "--userconfig",
	"--omit", "--include", "--save-prefix", "--filter", "-F", "-C", "--dir", "--cwd", "--scope",
	"--network-concurrency", "--modules-folder", "--loglevel")

// npxValueFlags adds the runners' flags: -p/--package name the package to
// run and -c a command string. For npm install, -p is a boolean.
var npxValueFlags = union(npmValueFlags, set("-p", "--package", "-c", "--call"))

// npmSpecs parses npm-style package specs. A runner (npx and friends)
// executes its first positional argument, or the --package values.
func npmSpecs(args []string, runner bool) []Request {
	flags := npmValueFlags
	if runner {
		flags = npxValueFlags
	}
	p := parseFlags(args, flags)
	private := p.has("--registry")
	specs := p.positional
	if runner {
		specs = p.all("-p", "--package")
		if len(specs) == 0 && len(p.positional) > 0 {
			specs = p.positional[:1]
		}
	}
	var out []Request
	for _, s := range specs {
		if name, version, ok := npmSpec(s); ok {
			out = append(out, Request{System: NPM, Name: name, Version: version, Private: private})
		}
	}
	return out
}

// npmSpec parses name, name@version, @scope/name@version and npm aliases
// (alias@npm:real@version). It rejects paths, URLs, git and tarball specs.
func npmSpec(s string) (name, version string, ok bool) {
	if !registrySpec(s) || strings.HasSuffix(s, ".tgz") || strings.HasSuffix(s, ".tar.gz") {
		return "", "", false
	}
	at := strings.Index(s, "@")
	if strings.HasPrefix(s, "@") {
		at = strings.Index(s[1:], "@")
		if at >= 0 {
			at++
		}
	}
	name = s
	if at >= 0 {
		name, version = s[:at], s[at+1:]
	}
	if strings.HasPrefix(version, "npm:") {
		return npmSpec(strings.TrimPrefix(version, "npm:"))
	}
	if !registrySpec(version) {
		return "", "", false
	}
	scoped := strings.HasPrefix(name, "@")
	if name == "" || strings.Count(name, "/") != b2i(scoped) {
		// An unscoped name with a slash is a GitHub shorthand (user/repo).
		return "", "", false
	}
	if scope, pkg, _ := strings.Cut(strings.TrimPrefix(name, "@"), "/"); scoped && (scope == "" || pkg == "") {
		return "", "", false
	}
	return strings.ToLower(name), version, true
}

// registrySpec rejects local paths and remote sources.
func registrySpec(s string) bool {
	if strings.HasPrefix(s, ".") || strings.HasPrefix(s, "/") || strings.HasPrefix(s, "~") || strings.Contains(s, "://") {
		return false
	}
	for _, p := range []string{"git+", "git:", "github:", "gitlab:", "bitbucket:", "file:", "link:", "workspace:", "portal:", "patch:", "http:", "https:"} {
		if strings.HasPrefix(s, p) {
			return false
		}
	}
	return true
}

var pipValueFlags = set("-r", "--requirement", "-c", "--constraint", "-e", "--editable", "-i", "--index-url",
	"--extra-index-url", "-f", "--find-links", "-t", "--target", "--prefix", "--root", "--python", "-p",
	"--upgrade-strategy", "--platform", "--python-version", "--implementation", "--abi", "--only-binary",
	"--no-binary", "--src", "--log", "--cache-dir", "--trusted-host", "--proxy", "--timeout", "--retries",
	"--index", "--default-index", "--group", "-G", "--optional", "--extra", "-E", "--extras", "--with",
	"--from", "--source", "--markers", "--spec", "--suffix", "--package", "--directory", "--project")

// pipSpecs parses pip-style requirement specs. A runner (uvx, pipx run,
// uv tool) runs its first positional argument, or the --from/--spec
// package; --with adds more packages to it.
func pipSpecs(args []string, runner bool) []Request {
	p := parseFlags(args, pipValueFlags)
	private := p.has("-i", "--index-url", "--extra-index-url", "-f", "--find-links", "--index", "--default-index", "--source")
	specs := p.positional
	if runner {
		specs = p.all("--from", "--spec")
		if len(specs) == 0 && len(p.positional) > 0 {
			specs = p.positional[:1]
		}
		specs = append(specs, p.all("--with")...)
	}
	var out []Request
	for _, s := range specs {
		if name, version, ok := pipSpec(s); ok {
			out = append(out, Request{System: PyPI, Name: name, Version: version, Private: private})
		}
	}
	return out
}

var pipRe = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._-]*)\s*(\[[^\]]*\])?\s*(.*)$`)

// pipSpec parses a requirement such as requests, requests==2.31.0,
// requests[socks]>=2 or poetry's requests@^2. The version is kept only
// when pinned exactly (==, or poetry's name@1.2.3).
func pipSpec(s string) (name, version string, ok bool) {
	s = strings.TrimSpace(s)
	if before, _, found := strings.Cut(s, ";"); found {
		s = strings.TrimSpace(before)
	}
	lower := strings.ToLower(s)
	if s == "" || !registrySpec(s) || strings.Contains(s, "/") || strings.Contains(s, "\\") ||
		strings.HasSuffix(lower, ".whl") || strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".zip") {
		return "", "", false
	}
	m := pipRe.FindStringSubmatch(s)
	if m == nil {
		return "", "", false
	}
	rest := strings.TrimSpace(m[3])
	switch {
	case strings.HasPrefix(rest, "==") && !strings.HasPrefix(rest, "==="):
		version, _, _ = strings.Cut(strings.TrimSpace(rest[2:]), ",")
		version = strings.TrimSpace(version)
	case strings.HasPrefix(rest, "@"):
		v := strings.TrimSpace(rest[1:])
		if v == "" || strings.Contains(v, ":") {
			return "", "", false // name @ url
		}
		version = v
	}
	if strings.ContainsAny(version, "*^~<>!, ") {
		version = ""
	}
	return NormalizePyPI(m[1]), version, true
}

// goSpecs parses go get / go install arguments: module or package paths
// with an optional @version. Local and standard-library paths are skipped.
func goSpecs(args []string) []Request {
	p := parseFlags(args, set("-modfile", "-C", "-p", "-tags", "-ldflags", "-gcflags", "-o"))
	var out []Request
	for _, s := range p.positional {
		path, version, _ := strings.Cut(s, "@")
		if !goPath(path) || version == "none" {
			continue
		}
		switch version {
		case "latest", "upgrade", "patch":
			version = ""
		}
		out = append(out, Request{System: Go, Name: strings.TrimSuffix(path, "/..."), Version: version})
	}
	return out
}

// goPath reports whether p names a remote module or package: not a local
// path, and its first element looks like a domain.
func goPath(p string) bool {
	if p == "" || !registrySpec(p) {
		return false
	}
	first, _, _ := strings.Cut(p, "/")
	return strings.Contains(first, ".")
}

var cargoValueFlags = set("-F", "--features", "--rename", "--registry", "--path", "--git", "--branch", "--tag",
	"--rev", "-p", "--package", "--manifest-path", "--target", "--version", "--vers", "--root", "--index",
	"--bin", "--example", "--profile", "-j", "--jobs", "--target-dir", "--config", "-Z")

// cargoSpecs parses cargo add / cargo install crates (name or
// name@version). Git and path sources are skipped.
func cargoSpecs(args []string, install bool) []Request {
	p := parseFlags(args, cargoValueFlags)
	if p.has("--git", "--path") {
		return nil
	}
	private := p.has("--registry", "--index")
	var out []Request
	for _, s := range p.positional {
		name, version, _ := strings.Cut(s, "@")
		if name == "" || !registrySpec(name) || strings.ContainsAny(name, "/\\") {
			continue
		}
		if v := p.all("--version", "--vers"); install && version == "" && len(v) > 0 {
			version = v[0]
		}
		out = append(out, Request{System: Cargo, Name: strings.ToLower(name), Version: version, Private: private})
	}
	return out
}

func set(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

func union(a, b map[string]bool) map[string]bool {
	m := make(map[string]bool, len(a)+len(b))
	for k := range a {
		m[k] = true
	}
	for k := range b {
		m[k] = true
	}
	return m
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
