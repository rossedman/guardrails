package deps

import (
	"reflect"
	"testing"
)

func TestFromManifest(t *testing.T) {
	t.Parallel()
	r := func(system, name, version, src string) Request {
		return Request{System: system, Name: name, Version: version, Source: src}
	}
	tests := []struct {
		name, rel, before, after string
		want                     []Request
	}{
		{
			"package.json adds and bumps", "web/package.json",
			`{"dependencies":{"react":"^18.2.0","zod":"3.22.0"}}`,
			`{"dependencies":{"react":"^18.2.0","zod":"3.23.0","Lodash":"*"},
			  "devDependencies":{"vitest":"latest","local":"file:../x","ws":"workspace:*","gh":"user/repo","git":"git+https://x/y.git"},
			  "peerDependencies":{"alias":"npm:real@1.0.0"},"optionalDependencies":"broken"}`,
			[]Request{r(NPM, "lodash", "", "web/package.json"), r(NPM, "real", "1.0.0", "web/package.json"), r(NPM, "vitest", "", "web/package.json"), r(NPM, "zod", "3.23.0", "web/package.json")},
		},
		{"package.json new file", "package.json", "", `{"dependencies":{"ms":"2.1.3"}}`, []Request{r(NPM, "ms", "2.1.3", "package.json")}},
		{"package.json invalid after", "package.json", `{}`, `{`, nil},
		{"package.json removal", "package.json", `{"dependencies":{"ms":"1"}}`, `{"dependencies":{}}`, []Request{}},
		{
			"requirements", "requirements-dev.txt",
			"requests==2.31.0\n",
			"# tools\nrequests==2.32.0  # bump\n-r base.txt\n--index-url https://x\n\nBlack>=24\n./local\n",
			[]Request{r(PyPI, "black", "", "requirements-dev.txt"), r(PyPI, "requests", "2.32.0", "requirements-dev.txt")},
		},
		{"requirements.in", "requirements.in", "", "httpx\n", []Request{r(PyPI, "httpx", "", "requirements.in")}},
		{
			"pyproject", "pyproject.toml",
			"[project]\ndependencies = [\"requests\"]\n",
			`[project]
dependencies = ["requests", "Rich==13.7.1"]
[project.optional-dependencies]
dev = ["pytest>=8"]
[dependency-groups]
lint = ["ruff", {include-group = "dev"}]
[tool.poetry.dependencies]
python = "^3.11"
Pendulum = "3.0.0"
local = {path = "../local"}
[tool.poetry.dev-dependencies]
mypy = {version = "^1.0"}
[tool.poetry.group.docs.dependencies]
mkdocs = "==1.6.0"
gitdep = {git = "https://x/y.git"}
`,
			[]Request{r(PyPI, "mkdocs", "1.6.0", "pyproject.toml"), r(PyPI, "mypy", "", "pyproject.toml"), r(PyPI, "pendulum", "3.0.0", "pyproject.toml"), r(PyPI, "pytest", "", "pyproject.toml"), r(PyPI, "rich", "13.7.1", "pyproject.toml"), r(PyPI, "ruff", "", "pyproject.toml")},
		},
		{"pyproject invalid", "pyproject.toml", "", "[project\n", nil},
		{
			"go.mod", "go.mod",
			"module x\n\nrequire github.com/a/b v1.0.0\n",
			"module x\n\nrequire github.com/a/b v1.1.0\n\nrequire (\n\tgolang.org/x/mod v0.41.0 // indirect\n\t// comment\n)\nreplace x => y\n",
			[]Request{r(Go, "github.com/a/b", "v1.1.0", "go.mod"), r(Go, "golang.org/x/mod", "v0.41.0", "go.mod")},
		},
		{
			"Cargo.toml", "crates/app/Cargo.toml", "",
			`[package]
name = "app"
[dependencies]
serde = "1"
tokio = { version = "1.38", features = ["full"] }
local = { path = "../local" }
shared = { workspace = true }
renamed = { package = "Real-Crate", version = "0.2" }
nover = { features = ["x"] }
[dev-dependencies]
proptest = "*"
[target.'cfg(unix)'.build-dependencies]
cc = "1.0"
`,
			[]Request{r(Cargo, "cc", "1.0", "crates/app/Cargo.toml"), r(Cargo, "nover", "", "crates/app/Cargo.toml"), r(Cargo, "proptest", "", "crates/app/Cargo.toml"), r(Cargo, "real-crate", "0.2", "crates/app/Cargo.toml"), r(Cargo, "serde", "1", "crates/app/Cargo.toml"), r(Cargo, "tokio", "1.38", "crates/app/Cargo.toml")},
		},
		{"Cargo.toml invalid", "Cargo.toml", "", "[dependencies\n", nil},
		{"other file", "src/main.go", "", "package main", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := FromManifest(tc.rel, tc.before, tc.after)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got  %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

func TestTableVersion(t *testing.T) {
	t.Parallel()
	if _, ok := tableVersion(3); ok {
		t.Error("a number is not a dependency spec")
	}
}

func TestSuggest(t *testing.T) {
	t.Parallel()
	tests := []struct{ system, name, want string }{
		{NPM, "lodash", ""},        // popular itself
		{NPM, "lodahs", "lodash"},  // transposition
		{NPM, "lodashh", "lodash"}, // insertion
		{NPM, "reactdom", "react-dom"},
		{NPM, "Lodash", "lodash"}, // case
		{PyPI, "reqeusts", "requests"},
		{PyPI, "reqests", "requests"}, // deletion
		{PyPI, "nump", ""},            // too short for edits
		{PyPI, "zzzz-unrelated-name", ""},
		{Go, "github.com/spf13/cobar", ""}, // no list
	}
	for _, tc := range tests {
		if got := Suggest(tc.system, tc.name); got != tc.want {
			t.Errorf("Suggest(%s, %q) = %q, want %q", tc.system, tc.name, got, tc.want)
		}
	}
}

func TestOneEdit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		a, b string
		want bool
	}{
		{"abc", "abc", false},
		{"abc", "abd", true},
		{"abc", "acb", true},
		{"abcd", "badc", false},
		{"abc", "ab", true},
		{"ab", "abc", true},
		{"abc", "a", false},
		{"abcde", "axcye", false},
		{"abc", "xbc", true},
	}
	for _, tc := range tests {
		if got := oneEdit(tc.a, tc.b); got != tc.want {
			t.Errorf("oneEdit(%q, %q) = %v", tc.a, tc.b, got)
		}
	}
}
