// Command gen refreshes the popular package lists the dependency check uses
// to spot typosquats: the most-downloaded npm and PyPI packages, one name
// per line. Run it from the repository root:
//
//	go run ./internal/deps/popular/gen
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	npmURL  = "https://cdn.jsdelivr.net/npm/npm-high-impact/lib/top-download.js"
	pypiURL = "https://raw.githubusercontent.com/hugovk/top-pypi-packages/main/top-pypi-packages.min.json"
	limit   = 5000
	outDir  = "internal/deps/popular"
)

var (
	quoted   = regexp.MustCompile(`'([^']+)'`)
	pep503re = regexp.MustCompile(`[-_.]+`)
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	err := run(ctx, npmURL, pypiURL, outDir)
	cancel()
	if err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
}

// run fetches both lists and writes them to dir.
func run(ctx context.Context, npmSrc, pypiSrc, dir string) error {
	js, err := fetch(ctx, npmSrc)
	if err != nil {
		return err
	}
	var npm []string
	for _, m := range quoted.FindAllStringSubmatch(string(js), -1) {
		npm = append(npm, m[1])
	}
	raw, err := fetch(ctx, pypiSrc)
	if err != nil {
		return err
	}
	var top struct {
		Rows []struct {
			Project string `json:"project"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(raw, &top); err != nil {
		return fmt.Errorf("decode %s: %w", pypiSrc, err)
	}
	var pypi []string
	for _, r := range top.Rows {
		pypi = append(pypi, pep503re.ReplaceAllString(strings.ToLower(r.Project), "-"))
	}
	if err := write(filepath.Join(dir, "npm.txt"), npm); err != nil {
		return err
	}
	return write(filepath.Join(dir, "pypi.txt"), pypi)
}

func fetch(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func write(path string, names []string) error {
	seen := map[string]bool{}
	var out []string
	for _, n := range names {
		if n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
		if len(out) == limit {
			break
		}
	}
	if len(out) < limit/2 {
		return fmt.Errorf("%s: only %d names, refusing to overwrite", path, len(out))
	}
	return os.WriteFile(path, []byte(strings.Join(out, "\n")+"\n"), 0o644)
}
