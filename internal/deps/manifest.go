package deps

import (
	"encoding/json"
	"path"
	"regexp"
	"sort"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// FromManifest returns the dependencies an edit to a manifest adds or
// re-versions: package.json, requirements files, pyproject.toml, go.mod
// and Cargo.toml. rel is the repo-relative, slash-separated path. Other
// files, and manifests that don't parse after the edit, return nil.
func FromManifest(rel, before, after string) []Request {
	base := path.Base(rel)
	var parse func(string) (map[string]string, bool)
	system := ""
	switch {
	case base == "package.json":
		parse, system = packageJSON, NPM
	case strings.Contains(strings.ToLower(base), "requirements") && (strings.HasSuffix(base, ".txt") || strings.HasSuffix(base, ".in")):
		parse, system = requirementsTxt, PyPI
	case base == "pyproject.toml":
		parse, system = pyprojectTOML, PyPI
	case base == "go.mod":
		parse, system = goMod, Go
	case base == "Cargo.toml":
		parse, system = cargoTOML, Cargo
	default:
		return nil
	}
	now, ok := parse(after)
	if !ok {
		return nil
	}
	was, _ := parse(before)
	names := make([]string, 0, len(now))
	for name, version := range now {
		if old, existed := was[name]; !existed || old != version {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	out := make([]Request, 0, len(names))
	for _, name := range names {
		out = append(out, Request{System: system, Name: name, Version: now[name], Source: rel})
	}
	return out
}

// packageJSON reads the registry dependencies of a package.json, with
// their version ranges.
func packageJSON(s string) (map[string]string, bool) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		return nil, false
	}
	out := map[string]string{}
	for _, section := range []string{"dependencies", "devDependencies", "optionalDependencies", "peerDependencies"} {
		var deps map[string]string
		if raw, ok := doc[section]; !ok || json.Unmarshal(raw, &deps) != nil {
			continue
		}
		for name, spec := range deps {
			n, v, ok := npmSpec(name + "@" + spec)
			if !ok || (strings.Contains(spec, "/") && !strings.HasPrefix(spec, "npm:")) {
				continue
			}
			out[n] = cleanRange(v)
		}
	}
	return out, true
}

// requirementsTxt reads pip requirement lines, skipping comments, options
// and references to other files.
func requirementsTxt(s string) (map[string]string, bool) {
	out := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		line, _, _ = strings.Cut(line, " #")
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") {
			continue
		}
		if name, version, ok := pipSpec(line); ok {
			out[name] = version
		}
	}
	return out, true
}

// pyprojectTOML reads PEP 621 dependencies and optional dependencies,
// PEP 735 dependency groups, and Poetry's dependency tables.
func pyprojectTOML(s string) (map[string]string, bool) {
	var doc struct {
		Project struct {
			Dependencies         []string            `toml:"dependencies"`
			OptionalDependencies map[string][]string `toml:"optional-dependencies"`
		} `toml:"project"`
		DependencyGroups map[string][]any `toml:"dependency-groups"`
		Tool             struct {
			Poetry struct {
				Dependencies    map[string]any `toml:"dependencies"`
				DevDependencies map[string]any `toml:"dev-dependencies"`
				Group           map[string]struct {
					Dependencies map[string]any `toml:"dependencies"`
				} `toml:"group"`
			} `toml:"poetry"`
		} `toml:"tool"`
	}
	if err := toml.Unmarshal([]byte(s), &doc); err != nil {
		return nil, false
	}
	out := map[string]string{}
	addSpec := func(spec string) {
		if name, version, ok := pipSpec(spec); ok {
			out[name] = version
		}
	}
	for _, d := range doc.Project.Dependencies {
		addSpec(d)
	}
	for _, group := range doc.Project.OptionalDependencies {
		for _, d := range group {
			addSpec(d)
		}
	}
	for _, group := range doc.DependencyGroups {
		for _, d := range group {
			if spec, ok := d.(string); ok { // tables are include-group references
				addSpec(spec)
			}
		}
	}
	poetry := []map[string]any{doc.Tool.Poetry.Dependencies, doc.Tool.Poetry.DevDependencies}
	for _, g := range doc.Tool.Poetry.Group {
		poetry = append(poetry, g.Dependencies)
	}
	for _, table := range poetry {
		for name, v := range table {
			version, ok := tableVersion(v)
			if !ok || strings.EqualFold(name, "python") {
				continue
			}
			out[NormalizePyPI(name)] = poetryVersion(version)
		}
	}
	return out, true
}

// poetryVersion keeps only exact versions: "1.2.3" or "==1.2.3".
func poetryVersion(v string) string {
	v = strings.TrimPrefix(strings.TrimSpace(v), "==")
	if exactVersion.MatchString(v) {
		return v
	}
	return ""
}

var exactVersion = regexp.MustCompile(`^v?\d+(\.\d+)*([-+.]?[0-9A-Za-z.-]+)?$`)

// tableVersion reads a TOML dependency value: a version string, or a table
// with a version key. Tables without one (path, git, workspace) aren't
// registry dependencies.
func tableVersion(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case map[string]any:
		if _, local := t["path"]; local {
			return "", false
		}
		if _, git := t["git"]; git {
			return "", false
		}
		if version, ok := t["version"].(string); ok {
			return version, true
		}
		if _, ws := t["workspace"]; ws {
			return "", false
		}
		return "", true
	}
	return "", false
}

var goRequire = regexp.MustCompile(`^(?:require\s+)?([^\s()]+)\s+(v[^\s]+)`)

// goMod reads the require directives of a go.mod file.
func goMod(s string) (map[string]string, bool) {
	out := map[string]string{}
	inBlock := false
	for _, line := range strings.Split(s, "\n") {
		line, _, _ = strings.Cut(line, "//")
		line = strings.TrimSpace(line)
		switch {
		case line == "require (":
			inBlock = true
			continue
		case inBlock && line == ")":
			inBlock = false
			continue
		case !inBlock && !strings.HasPrefix(line, "require "):
			continue
		}
		if m := goRequire.FindStringSubmatch(line); m != nil {
			out[m[1]] = m[2]
		}
	}
	return out, true
}

// cargoTOML reads [dependencies], [dev-dependencies] and
// [build-dependencies], including per-target tables.
func cargoTOML(s string) (map[string]string, bool) {
	type tables struct {
		Dependencies      map[string]any `toml:"dependencies"`
		DevDependencies   map[string]any `toml:"dev-dependencies"`
		BuildDependencies map[string]any `toml:"build-dependencies"`
	}
	var doc struct {
		tables
		Target map[string]tables `toml:"target"`
	}
	if err := toml.Unmarshal([]byte(s), &doc); err != nil {
		return nil, false
	}
	all := []tables{doc.tables}
	for _, t := range doc.Target {
		all = append(all, t)
	}
	out := map[string]string{}
	for _, t := range all {
		for _, table := range []map[string]any{t.Dependencies, t.DevDependencies, t.BuildDependencies} {
			for name, v := range table {
				if version, ok := tableVersion(v); ok {
					if pkg, isTable := v.(map[string]any); isTable {
						if real, renamed := pkg["package"].(string); renamed {
							name = real
						}
					}
					out[strings.ToLower(name)] = cleanRange(version)
				}
			}
		}
	}
	return out, true
}

// cleanRange turns "latest" and "*" into "" (any version); other ranges
// are kept for the checker to resolve.
func cleanRange(v string) string {
	v = strings.TrimSpace(v)
	switch v {
	case "", "*", "latest", "x":
		return ""
	}
	return v
}
