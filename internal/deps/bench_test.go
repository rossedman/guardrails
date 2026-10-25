package deps

import (
	"strings"
	"testing"
)

var benchCommands = []string{
	"npm install --save-dev @types/node@20.1.0 left-pad@^1 && npx -y -p typescript@5 tsc --version",
	"pip3 install Flask_Login==0.6.3 'uvicorn[standard]>=0.20' numpy==1.* && python -m pip install httpx",
	"cd app && go get -u golang.org/x/tools/...@latest example.com/m@upgrade | tee out.log",
	"cargo add serde --features derive tokio@1; uvx --with rich pycowsay hello",
	"make build && go test -race ./... && git status --short",
}

func BenchmarkFromCommand(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		for _, c := range benchCommands {
			_ = FromCommand(c)
		}
	}
}

func benchPackageJSON(n int, version string) string {
	var sb strings.Builder
	sb.WriteString(`{"name":"app","dependencies":{`)
	for i := range n {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`"pkg-` + strings.Repeat("x", i%7) + string(rune('a'+i%26)) + string(rune('a'+i/26%26)) + `":"` + version + `"`)
	}
	sb.WriteString(`}}`)
	return sb.String()
}

func BenchmarkFromManifest(b *testing.B) {
	before, after := benchPackageJSON(200, "^1.0.0"), benchPackageJSON(200, "^1.1.0")
	reqs := "requests==2.31.0\nBlack>=24\n-r base.txt\n"
	gomod := "module x\n\nrequire (\n\tgithub.com/a/b v1.1.0\n\tgolang.org/x/mod v0.41.0 // indirect\n)\n"
	b.ReportAllocs()
	for b.Loop() {
		_ = FromManifest("web/package.json", before, after)
		_ = FromManifest("requirements.txt", "", reqs)
		_ = FromManifest("go.mod", "module x\n", gomod)
	}
}

func BenchmarkSuggest(b *testing.B) {
	names := []struct{ system, name string }{
		{NPM, "lodahs"},         // transposition of a popular name
		{PyPI, "reqeusts"},      // transposition
		{NPM, "left-pad-extra"}, // full scan, no match
		{PyPI, "my-internal-lib"},
		{NPM, "react"}, // popular: immediate exit
	}
	b.ReportAllocs()
	for b.Loop() {
		for _, n := range names {
			_ = Suggest(n.system, n.name)
		}
	}
}
