package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rossedman/guardrails/internal/tools"
)

var exe = []byte("#!/bin/sh\necho hi\n")

func hexSum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func wheel(t *testing.T, member string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(member)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(exe); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// fixture serves raw binaries, wheels and a PyPI JSON API.
type fixture struct {
	srv   *httptest.Server
	files map[string][]byte
	pypi  map[string]any
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{files: map[string][]byte{}, pypi: map[string]any{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if body, ok := f.pypi[r.URL.Path]; ok {
			_ = json.NewEncoder(w).Encode(body)
			return
		}
		if data, ok := f.files[r.URL.Path]; ok {
			_, _ = w.Write(data)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(f.srv.Close)
	oldBase, oldCompile := pypiBase, compileReqs
	pypiBase = f.srv.URL
	compileReqs = func(_ context.Context, req string) (string, error) {
		if strings.HasPrefix(req, "broken==") {
			return "", errors.New("resolution failed")
		}
		return req + " --hash=sha256:" + strings.Repeat("c", 64) + "\n", nil
	}
	t.Cleanup(func() { pypiBase, compileReqs = oldBase, oldCompile })
	return f
}

// addWheels publishes a PyPI release with one wheel per tag.
func (f *fixture) addWheels(t *testing.T, pkg, version, member string, tags []string, badDigest bool) {
	t.Helper()
	var urls []map[string]any
	for _, tag := range tags {
		name := fmt.Sprintf("%s-%s-py3-none-%s.whl", pkg, version, tag)
		data := wheel(t, member)
		f.files["/files/"+name] = data
		digest := hexSum(data)
		if badDigest {
			digest = strings.Repeat("0", 64)
		}
		urls = append(urls, map[string]any{"filename": name, "url": f.srv.URL + "/files/" + name, "digests": map[string]string{"sha256": digest}})
	}
	urls = append(urls, map[string]any{"filename": pkg + "-" + version + ".tar.gz", "url": "x", "digests": map[string]string{"sha256": "y"}})
	f.pypi["/pypi/"+pkg+"/"+version+"/json"] = map[string]any{"urls": urls}
}

const specYAML = `platforms: [linux/amd64, darwin/arm64]
tools:
  rawtool:
    version: 1.0.0
    source: archive
    url: "%[1]s/raw/rawtool_{version}_{os}_{arch}"
    format: raw
    os: {darwin: macos}
    arch: {amd64: x86_64}
  wheeltool:
    version: 2.0.0
    source: pypi
    package: wheeltool
    path: "wheeltool-{version}.data/scripts/wheeltool"
    wheels: {linux/amd64: manylinux_x86_64, darwin/arm64: macosx_arm64}
  pytool: {version: 3.0.0, source: uv, package: pytool}
  aliased: {version: 3.1.0, source: uv, package: pkg, entrypoint: cli}
  gotool: {version: 0.1.0, source: go, module: example.com/cmd/gotool}
`

func writeSpec(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, "tools.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRunGeneratesLock(t *testing.T) {
	f := newFixture(t)
	f.files["/raw/rawtool_1.0.0_linux_x86_64"] = exe
	f.files["/raw/rawtool_1.0.0_macos_arm64"] = exe
	f.addWheels(t, "wheeltool", "2.0.0", "wheeltool-2.0.0.data/scripts/wheeltool", []string{"manylinux_x86_64", "macosx_arm64"}, false)

	dir := t.TempDir()
	in := writeSpec(t, dir, fmt.Sprintf(specYAML, f.srv.URL))
	out := filepath.Join(dir, "lock.json")
	if err := run(in, out, "", false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := tools.ParseLock(data)
	if err != nil {
		t.Fatal(err)
	}
	raw := lock.Tools["rawtool"].Platforms["darwin/arm64"]
	if raw.URL != f.srv.URL+"/raw/rawtool_1.0.0_macos_arm64" || raw.SHA256 != hexSum(exe) || raw.Path != "" {
		t.Errorf("raw artifact = %+v", raw)
	}
	whl := lock.Tools["wheeltool"].Platforms["linux/amd64"]
	if whl.Format != tools.FormatZip || whl.Path != "wheeltool-2.0.0.data/scripts/wheeltool" || len(whl.SHA256) != 64 {
		t.Errorf("wheel artifact = %+v", whl)
	}
	if py := lock.Tools["pytool"]; py.Entrypoint != "pytool" || !strings.Contains(py.Requirements, "pytool==3.0.0 --hash=sha256:") {
		t.Errorf("uv tool = %+v", py)
	}
	if lock.Tools["aliased"].Entrypoint != "cli" {
		t.Errorf("entrypoint = %q", lock.Tools["aliased"].Entrypoint)
	}
	if lock.Tools["gotool"].Module != "example.com/cmd/gotool" {
		t.Errorf("go tool = %+v", lock.Tools["gotool"])
	}
	if err := run(in, out, "", true); err != nil {
		t.Errorf("fresh lock fails check: %v", err)
	}

	// -only regenerates one tool and keeps the others, even if their
	// sources became unreachable.
	delete(f.files, "/raw/rawtool_1.0.0_linux_x86_64")
	if err := run(in, out, "gotool, pytool", false); err != nil {
		t.Fatalf("only: %v", err)
	}
	data2, _ := os.ReadFile(out)
	lock2, _ := tools.ParseLock(data2)
	if lock2.Tools["rawtool"].Platforms["linux/amd64"].SHA256 != hexSum(exe) {
		t.Error("-only dropped an untouched tool")
	}
}

func TestRunFailures(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()

	if err := run(filepath.Join(dir, "missing.yaml"), filepath.Join(dir, "o.json"), "", false); err == nil {
		t.Error("missing spec")
	}
	bad := writeSpec(t, dir, "platforms: []\ntools: {x: {version: 1, nope: true}}\n")
	if err := run(bad, filepath.Join(dir, "o.json"), "", false); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("unknown key: %v", err)
	}

	// A failing tool keeps its previous entry when the version is unchanged.
	f.files["/raw/rawtool_1.0.0_linux_x86_64"] = exe
	f.files["/raw/rawtool_1.0.0_macos_arm64"] = exe
	spec := `platforms: [linux/amd64, darwin/arm64]
tools:
  rawtool:
    version: 1.0.0
    source: archive
    url: "` + f.srv.URL + `/raw/rawtool_{version}_{os}_{arch}"
    format: raw
    os: {darwin: macos}
    arch: {amd64: x86_64}
  broken: {version: 1.0.0, source: uv, package: broken}
`
	in := writeSpec(t, dir, spec)
	out := filepath.Join(dir, "lock.json")
	err := run(in, out, "", false)
	if err == nil || !strings.Contains(err.Error(), "could not lock: broken") {
		t.Fatalf("err = %v", err)
	}
	prev := `{"tools":{"broken":{"version":"1.0.0","source":"uv","requirements":"old","entrypoint":"broken"}}}`
	if err := os.WriteFile(out, []byte(prev), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = run(in, out, "", false)
	data, _ := os.ReadFile(out)
	if !strings.Contains(string(data), `"requirements": "old"`) {
		t.Errorf("previous entry not kept:\n%s", data)
	}

	if err := run(in, filepath.Join(dir, "no", "such", "dir", "lock.json"), "rawtool", false); err == nil {
		t.Error("unwritable output")
	}
}

func TestLockToolErrors(t *testing.T) {
	f := newFixture(t)
	f.addWheels(t, "w", "1", "w/bin", []string{"linux"}, false)
	f.addWheels(t, "bad", "1", "bad/bin", []string{"linux"}, true)
	f.addWheels(t, "nomember", "1", "elsewhere", []string{"linux"}, false)
	ctx := context.Background()
	cases := []struct {
		name string
		t    tool
		want string
	}{
		{"unknown source", tool{Version: "1", Source: "npm"}, "unknown source"},
		{"archive 404", tool{Version: "1", Source: tools.SourceArchive, URL: f.srv.URL + "/none", Format: tools.FormatRaw}, "404"},
		{"pypi missing release", tool{Version: "9", Source: tools.SourcePyPI, Package: "w"}, "404"},
		{"pypi no tag", tool{Version: "1", Source: tools.SourcePyPI, Package: "w", Wheels: map[string]string{}}, "no wheel tag"},
		{"pypi no match", tool{Version: "1", Source: tools.SourcePyPI, Package: "w", Wheels: map[string]string{"linux/amd64": "macos"}}, "no wheel matching"},
		{"pypi digest", tool{Version: "1", Source: tools.SourcePyPI, Package: "bad", Path: "bad/bin", Wheels: map[string]string{"linux/amd64": "linux"}}, "does not match"},
		{"pypi member", tool{Version: "1", Source: tools.SourcePyPI, Package: "nomember", Path: "w/bin", Wheels: map[string]string{"linux/amd64": "linux"}}, "not found in archive"},
		{"uv compile", tool{Version: "1", Source: tools.SourceUV, Package: "broken"}, "resolution failed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := lockTool(ctx, []string{"linux/amd64"}, "x", c.t)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
		})
	}
	t.Run("bad pypi json", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("{")) }))
		defer srv.Close()
		pypiBase = srv.URL
		if _, err := pypiFiles(ctx, "x", "1"); err == nil {
			t.Error("expected decode error")
		}
		pypiBase = "http://[::1]:bad"
		if _, err := pypiFiles(ctx, "x", "1"); err == nil {
			t.Error("expected request error")
		}
		pypiBase = "http://127.0.0.1:1"
		if _, err := pypiFiles(ctx, "x", "1"); err == nil {
			t.Error("expected connection error")
		}
	})
	t.Run("fetch errors", func(t *testing.T) {
		if _, err := fetchAndVerify(ctx, tools.Artifact{URL: "http://[::1]:bad"}); err == nil {
			t.Error("bad url")
		}
		if _, err := fetchAndVerify(ctx, tools.Artifact{URL: "http://127.0.0.1:1/x"}); err == nil {
			t.Error("unreachable")
		}
	})
}

func TestCheckLock(t *testing.T) {
	t.Parallel()
	s := spec{
		Platforms: []string{"linux/amd64", "darwin/arm64"},
		Tools: map[string]tool{
			"a": {Version: "1", Source: tools.SourceArchive},
			"b": {Version: "2", Source: tools.SourceGo},
			"c": {Version: "3", Source: tools.SourceUV},
		},
	}
	art := tools.Artifact{URL: "u", SHA256: strings.Repeat("a", 64), Format: tools.FormatRaw}
	good := &tools.Lock{Tools: map[string]tools.Tool{
		"a": {Version: "1", Source: tools.SourceArchive, Platforms: map[string]tools.Artifact{"linux/amd64": art, "darwin/arm64": art}},
		"b": {Version: "2", Source: tools.SourceGo, Module: "m"},
		"c": {Version: "3", Source: tools.SourceUV, Requirements: "r", Entrypoint: "c"},
	}}
	if err := checkLock(s, good); err != nil {
		t.Fatalf("good lock: %v", err)
	}
	stale := &tools.Lock{Tools: map[string]tools.Tool{
		"a":     {Version: "1", Source: tools.SourceArchive, Platforms: map[string]tools.Artifact{"linux/amd64": art}},
		"b":     {Version: "1", Source: tools.SourceGo, Module: "m"},
		"extra": {Version: "1", Source: tools.SourceGo, Module: "m"},
	}}
	err := checkLock(s, stale)
	if err == nil {
		t.Fatal("stale lock passed")
	}
	for _, want := range []string{"a: no artifact for darwin/arm64", "b: lock has 1 (go), spec wants 2 (go)", "c: not locked", "extra: locked but not in the spec"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in:\n%v", want, err)
		}
	}
}

func TestRender(t *testing.T) {
	t.Parallel()
	tl := tool{Version: "1.2.3", OS: map[string]string{"linux": "Linux"}, Arch: map[string]string{"arm64": "ARM64"}}
	cases := map[string]string{
		"linux/arm64":  "x_1.2.3_Linux-ARM64",
		"darwin/amd64": "x_1.2.3_darwin-amd64",
	}
	for platform, want := range cases {
		if got := render("x_{version}_{os}-{arch}", tl, platform); got != want {
			t.Errorf("%s: %s, want %s", platform, got, want)
		}
	}
}

func TestPickWheel(t *testing.T) {
	t.Parallel()
	files := []pypiFile{
		{Filename: "pkg-1.tar.gz"},
		{Filename: "pkg-1-py3-none-manylinux_x86_64.whl", URL: "a"},
		{Filename: "pkg-1-py3-none-macosx_arm64.whl", URL: "b"},
	}
	if f, ok := pickWheel(files, "macosx_arm64"); !ok || f.URL != "b" {
		t.Errorf("got %+v %v", f, ok)
	}
	if _, ok := pickWheel(files, "tar.gz"); ok {
		t.Error("sdist matched")
	}
}

func TestCompile(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\ncat >/dev/null\necho \"pinned==1.0 --hash=sha256:abc\"\n"
	if err := os.WriteFile(filepath.Join(dir, "uv"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	got, err := compile(context.Background(), "pinned==1.0")
	if err != nil || !strings.Contains(got, "--hash=sha256:abc") {
		t.Fatalf("got %q, %v", got, err)
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := compile(context.Background(), "x==1"); err == nil || !strings.Contains(err.Error(), "uv pip compile") {
		t.Errorf("err = %v", err)
	}
}
