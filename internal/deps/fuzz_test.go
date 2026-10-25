package deps

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/rossedman/guardrails/internal/guard"
)

// installBins are the binaries fromArgs recognizes; a command naming none of
// them cannot yield a request.
var installBins = []string{"npm", "pnpm", "yarn", "bun", "npx", "pnpx", "bunx", "pip", "python", "py", "uv", "poetry", "pipx", "go", "cargo"}

func FuzzFromCommand(f *testing.F) {
	for _, s := range append([]string{
		"npm install lodash",
		"npm i -D @types/node@20.1.0 Left-Pad@^1",
		"npm install --registry https://npm.acme.dev internal-pkg",
		"npx -y -p typescript@5 tsc --version",
		"pip3 install Flask_Login==0.6.3 'uvicorn[standard]>=0.20' numpy==1.*",
		"python -m pip install httpx && uvx --with rich pycowsay hello",
		"go get -u golang.org/x/tools/...@latest example.com/m@upgrade",
		"cargo add serde --features derive tokio@1",
		"echo npm install lodash",
		"npm install -- --weird-name",
		"make build",
	}, benchCommands...) {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, cmd string) {
		got := FromCommand(cmd)
		if !reflect.DeepEqual(got, FromCommand(cmd)) {
			t.Fatalf("FromCommand(%q) is not deterministic", cmd)
		}
		// Match against the words the shell would run, after quote removal:
		// g\o get is go get.
		var words string
		for _, args := range guard.Commands(cmd) {
			words += strings.Join(args, " ") + "\n"
		}
		if !slices.ContainsFunc(installBins, func(b string) bool { return strings.Contains(words, b) }) && len(got) > 0 {
			t.Fatalf("FromCommand(%q) = %+v without any install binary", cmd, got)
		}
		for _, r := range got {
			if r.Name == "" || r.Source == "" {
				t.Fatalf("FromCommand(%q) returned incomplete request %+v", cmd, r)
			}
			switch r.System {
			case NPM:
				if r.Name != strings.ToLower(r.Name) {
					t.Fatalf("FromCommand(%q): npm name %q not lowercased", cmd, r.Name)
				}
			case PyPI:
				if r.Name != NormalizePyPI(r.Name) {
					t.Fatalf("FromCommand(%q): pypi name %q not normalized", cmd, r.Name)
				}
			case Go, Cargo:
			default:
				t.Fatalf("FromCommand(%q): unknown system in %+v", cmd, r)
			}
		}
	})
}

func FuzzFromManifest(f *testing.F) {
	f.Add("web/package.json", `{"dependencies":{"react":"^18.2.0","zod":"3.22.0"}}`, `{"dependencies":{"react":"^18.2.0","zod":"3.23.0","Lodash":"*"},"peerDependencies":{"alias":"npm:real@1.0.0"}}`)
	f.Add("requirements-dev.txt", "requests==2.31.0\n", "# tools\nrequests==2.32.0  # bump\n-r base.txt\nBlack>=24\n./local\n")
	f.Add("pyproject.toml", "[project]\ndependencies = [\"requests\"]\n", "[project]\ndependencies = [\"requests\", \"Rich==13.7.1\"]\n[tool.poetry.dependencies]\nPendulum = \"3.0.0\"\n")
	f.Add("go.mod", "module x\n\nrequire github.com/a/b v1.0.0\n", "module x\n\nrequire github.com/a/b v1.1.0\n\nrequire (\n\tgolang.org/x/mod v0.41.0 // indirect\n)\n")
	f.Add("crates/app/Cargo.toml", "", "[dependencies]\nserde = \"1\"\ntokio = { version = \"1.38\" }\nrenamed = { package = \"Real-Crate\", version = \"0.2\" }\n")
	f.Add("src/main.go", "", "package main")
	f.Fuzz(func(t *testing.T, rel, before, after string) {
		got := FromManifest(rel, before, after)
		if !reflect.DeepEqual(got, FromManifest(rel, before, after)) {
			t.Fatalf("FromManifest(%q) is not deterministic", rel)
		}
		if len(FromManifest(rel, after, after)) != 0 {
			t.Fatalf("FromManifest(%q) with unchanged manifest returned requests", rel)
		}
		var prev string
		for i, r := range got {
			if r.Name == "" || r.Source != rel || r.System == "" {
				t.Fatalf("FromManifest(%q) returned incomplete request %+v", rel, r)
			}
			if i > 0 && r.Name <= prev {
				t.Fatalf("FromManifest(%q) names not sorted and unique: %q after %q", rel, r.Name, prev)
			}
			prev = r.Name
		}
	})
}

func FuzzSuggest(f *testing.F) {
	for _, n := range []struct{ system, name string }{
		{NPM, "lodahs"},
		{NPM, "react"},
		{NPM, "React_DOM"},
		{PyPI, "reqeusts"},
		{PyPI, "requests"},
		{PyPI, "my-internal-lib"},
		{Go, "github.com/spf13/cobra"},
		{Cargo, "serde"},
		{NPM, ""},
	} {
		f.Add(n.system, n.name)
	}
	f.Fuzz(func(t *testing.T, system, name string) {
		got := Suggest(system, name)
		if got != Suggest(system, name) {
			t.Fatalf("Suggest(%q, %q) is not deterministic", system, name)
		}
		if got == "" {
			return
		}
		list, ok := popular[system]
		if !ok || !list.set[got] {
			t.Fatalf("Suggest(%q, %q) = %q, which is not a popular %s package", system, name, got, system)
		}
		if got == name {
			t.Fatalf("Suggest(%q, %q) suggested the name itself", system, name)
		}
		if squash(got) != squash(name) && !oneEdit(name, got) {
			t.Fatalf("Suggest(%q, %q) = %q, which is not close", system, name, got)
		}
	})
}
