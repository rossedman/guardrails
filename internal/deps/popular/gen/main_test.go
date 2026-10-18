package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// lists serves an npm-high-impact style JS list and a top-pypi-packages
// style JSON document with n names each.
func lists(t *testing.T, n int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b strings.Builder
		switch r.URL.Path {
		case "/npm.js":
			b.WriteString("export const topDownload = [\n")
			for i := range n {
				fmt.Fprintf(&b, "  'pkg-%d',\n  'pkg-%d',\n", i, i)
			}
			b.WriteString("]\n")
		case "/pypi.json":
			b.WriteString(`{"rows": [`)
			for i := range n {
				if i > 0 {
					b.WriteString(",")
				}
				fmt.Fprintf(&b, `{"project": "Py_Pkg.%d"}`, i)
			}
			b.WriteString("]}")
		case "/bad.json":
			b.WriteString("{")
		default:
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(b.String()))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRun(t *testing.T) {
	srv := lists(t, limit+10)
	dir := t.TempDir()
	if err := run(context.Background(), srv.URL+"/npm.js", srv.URL+"/pypi.json", dir); err != nil {
		t.Fatal(err)
	}
	npm, _ := os.ReadFile(filepath.Join(dir, "npm.txt"))
	pypi, _ := os.ReadFile(filepath.Join(dir, "pypi.txt"))
	if got := strings.Count(string(npm), "\n"); got != limit || !strings.HasPrefix(string(npm), "pkg-0\npkg-1\n") {
		t.Errorf("npm.txt: %d lines, starts %q", got, string(npm)[:12])
	}
	if !strings.HasPrefix(string(pypi), "py-pkg-0\n") {
		t.Errorf("pypi.txt not PEP 503 normalized: %q", string(pypi)[:12])
	}
}

func TestRunErrors(t *testing.T) {
	srv := lists(t, limit)
	small := lists(t, 3)
	dir := t.TempDir()
	ctx := context.Background()
	for name, tc := range map[string]struct{ npm, pypi, dir string }{
		"npm missing":  {srv.URL + "/nope", srv.URL + "/pypi.json", dir},
		"pypi missing": {srv.URL + "/npm.js", srv.URL + "/nope", dir},
		"pypi bad":     {srv.URL + "/npm.js", srv.URL + "/bad.json", dir},
		"too few":      {small.URL + "/npm.js", srv.URL + "/pypi.json", dir},
		"no dir":       {srv.URL + "/npm.js", srv.URL + "/pypi.json", filepath.Join(dir, "missing")},
		"bad url":      {"://x", srv.URL + "/pypi.json", dir},
		"unreachable":  {"http://127.0.0.1:1/npm.js", srv.URL + "/pypi.json", dir},
	} {
		if err := run(ctx, tc.npm, tc.pypi, tc.dir); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}
