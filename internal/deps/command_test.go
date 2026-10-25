package deps

import (
	"reflect"
	"testing"
)

func TestFromCommand(t *testing.T) {
	t.Parallel()
	r := func(system, name, version string) Request {
		return Request{System: system, Name: name, Version: version}
	}
	priv := func(q Request) Request { q.Private = true; return q }
	tests := []struct {
		cmd  string
		want []Request
	}{
		// npm and friends
		{"npm install lodash", []Request{r(NPM, "lodash", "")}},
		{"npm i -D @types/node@20.1.0 Left-Pad@^1", []Request{r(NPM, "@types/node", "20.1.0"), r(NPM, "left-pad", "^1")}},
		{"npm install", nil},
		{"npm install ./local ../x /abs ~/y file:z git+https://g/x.git https://x.com/a.tgz user/repo github:u/r pkg.tgz", nil},
		{"npm install --registry https://npm.acme.dev internal-pkg", []Request{priv(r(NPM, "internal-pkg", ""))}},
		{"npm install --tag beta -w app react", []Request{r(NPM, "react", "")}},
		{"npm install -p left-pad", []Request{r(NPM, "left-pad", "")}},
		{"npm add alias@npm:real-pkg@2.0.0", []Request{r(NPM, "real-pkg", "2.0.0")}},
		{"npm run build", nil},
		{"npm exec cowsay", []Request{r(NPM, "cowsay", "")}},
		{"npx create-react-app my-app", []Request{r(NPM, "create-react-app", "")}},
		{"npx -y -p typescript@5 tsc --version", []Request{r(NPM, "typescript", "5")}},
		{"npx --package=eslint eslint .", []Request{r(NPM, "eslint", "")}},
		{"npx", nil},
		{"pnpm add -D vitest", []Request{r(NPM, "vitest", "")}},
		{"pnpm --filter web add zod", nil},
		{"pnpm dlx degit user/repo", []Request{r(NPM, "degit", "")}},
		{"pnpx prisma", []Request{r(NPM, "prisma", "")}},
		{"yarn add express@4", []Request{r(NPM, "express", "4")}},
		{"yarn global add serve", []Request{r(NPM, "serve", "")}},
		{"yarn dlx @scope/cli", []Request{r(NPM, "@scope/cli", "")}},
		{"yarn install", nil},
		{"bun add hono", []Request{r(NPM, "hono", "")}},
		{"bun x vite", []Request{r(NPM, "vite", "")}},
		{"bunx shadcn@latest init", []Request{r(NPM, "shadcn", "latest")}},
		{"/usr/local/bin/npm install ms", []Request{r(NPM, "ms", "")}},
		{"npm install @ @/x @x/", nil},
		// pip and friends
		{"pip install requests", []Request{r(PyPI, "requests", "")}},
		{"pip3 install Flask_Login==0.6.3 'uvicorn[standard]>=0.20' numpy==1.*", []Request{r(PyPI, "flask-login", "0.6.3"), r(PyPI, "uvicorn", ""), r(PyPI, "numpy", "")}},
		{"pip install -r requirements.txt -e . -c constraints.txt", nil},
		{"pip install -i https://pypi.acme.dev/simple acme-lib", []Request{priv(r(PyPI, "acme-lib", ""))}},
		{"pip install --extra-index-url=https://x acme", []Request{priv(r(PyPI, "acme", ""))}},
		{"pip install ./dist/pkg.whl pkg.tar.gz https://x/y.zip pkg@https://x", nil},
		{"pip install 'name @ https://x/y.whl'", nil},
		{"pip install \"black ; python_version>'3'\"", []Request{r(PyPI, "black", "")}},
		{"pip freeze", nil},
		{"python -m pip install httpx", []Request{r(PyPI, "httpx", "")}},
		{"python3 -m venv .venv", nil},
		{"uv pip install ruff==0.6.0", []Request{r(PyPI, "ruff", "0.6.0")}},
		{"uv pip list", nil},
		{"uv add --dev pytest --frozen", []Request{r(PyPI, "pytest", "")}},
		{"uv add --index acme=https://x acme", []Request{priv(r(PyPI, "acme", ""))}},
		{"uv tool install ruff", []Request{r(PyPI, "ruff", "")}},
		{"uv tool list", nil},
		{"uv sync", nil},
		{"uvx --from httpie http GET x", []Request{r(PyPI, "httpie", "")}},
		{"uvx --with rich pycowsay hello", []Request{r(PyPI, "pycowsay", ""), r(PyPI, "rich", "")}},
		{"uvx", nil},
		{"poetry add pendulum@^2.0 django@4.2.1", []Request{r(PyPI, "pendulum", ""), r(PyPI, "django", "4.2.1")}},
		{"poetry install", nil},
		{"pipx install black", []Request{r(PyPI, "black", "")}},
		{"pipx run cowsay moo", []Request{r(PyPI, "cowsay", "")}},
		// go
		{"go get github.com/spf13/cobra@v1.10.2", []Request{r(Go, "github.com/spf13/cobra", "v1.10.2")}},
		{"go get -u golang.org/x/tools/...@latest example.com/m@upgrade", []Request{r(Go, "golang.org/x/tools", ""), r(Go, "example.com/m", "")}},
		{"go get ./... fmt mymod/pkg github.com/x/y@none", nil},
		{`g\o get "github.com/spf13/cobra"`, []Request{r(Go, "github.com/spf13/cobra", "")}}, // quote removal: still go get
		{"go install -modfile tools.mod honnef.co/go/tools/cmd/staticcheck@2024.1", []Request{r(Go, "honnef.co/go/tools/cmd/staticcheck", "2024.1")}},
		{"go build ./...", nil},
		{"go", nil},
		// cargo
		{"cargo add serde --features derive tokio@1", []Request{r(Cargo, "serde", ""), r(Cargo, "tokio", "1")}},
		{"cargo add --git https://github.com/x/y y", nil},
		{"cargo add --registry acme acme-core", []Request{priv(r(Cargo, "acme-core", ""))}},
		{"cargo install ripgrep --version 14.1.0", []Request{r(Cargo, "ripgrep", "14.1.0")}},
		{"cargo install --path .", nil},
		{"cargo build", nil},
		// other commands, and the -- terminator
		{"echo npm install lodash", nil},
		{"npm install -- --weird-name", []Request{r(NPM, "--weird-name", "")}},
	}
	for _, tc := range tests {
		got := FromCommand(tc.cmd)
		for i := range got {
			got[i].Source = ""
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("FromCommand(%q)\n got %+v\nwant %+v", tc.cmd, got, tc.want)
		}
	}
}

func TestFromCommandSource(t *testing.T) {
	t.Parallel()
	got := FromCommand("cd web && sudo npm install zod")
	if len(got) != 1 || got[0].Source != "npm install zod" {
		t.Errorf("got %+v", got)
	}
}

func TestRequestString(t *testing.T) {
	t.Parallel()
	if s := (Request{Name: "a"}).String(); s != "a" {
		t.Error(s)
	}
	if s := (Request{Name: "a", Version: "1"}).String(); s != "a@1" {
		t.Error(s)
	}
}
