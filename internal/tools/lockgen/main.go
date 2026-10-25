// Command lockgen regenerates internal/tools/tools.lock.json from
// internal/tools/tools.yaml: it resolves every artifact URL per platform,
// downloads it, records its SHA-256, and checks the executable is where
// tools.yaml says. Python tools get a hash-pinned requirements file from
// `uv pip compile`.
//
//	go run ./internal/tools/lockgen            # regenerate everything
//	go run ./internal/tools/lockgen -only ruff # regenerate some tools
//	go run ./internal/tools/lockgen -check     # verify the lock matches tools.yaml (no network)
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/rossedman/guardrails/internal/tools"
)

type spec struct {
	Platforms []string        `yaml:"platforms"`
	Tools     map[string]tool `yaml:"tools"`
}

type tool struct {
	Version    string            `yaml:"version"`
	Source     string            `yaml:"source"`
	URL        string            `yaml:"url"`
	Format     string            `yaml:"format"`
	Path       string            `yaml:"path"`
	OS         map[string]string `yaml:"os"`
	Arch       map[string]string `yaml:"arch"`
	Package    string            `yaml:"package"`
	Wheels     map[string]string `yaml:"wheels"`
	Entrypoint string            `yaml:"entrypoint"`
	Module     string            `yaml:"module"`
}

// main parses the flags and runs lockgen, exiting 1 on any error.
func main() {
	in := flag.String("in", "internal/tools/tools.yaml", "tool specification")
	out := flag.String("out", "internal/tools/tools.lock.json", "lock file to write")
	only := flag.String("only", "", "comma-separated tools to regenerate; others are kept")
	check := flag.Bool("check", false, "verify the lock matches the spec without network access")
	flag.Parse()
	if err := run(*in, *out, *only, *check); err != nil {
		fmt.Fprintln(os.Stderr, "lockgen:", err)
		os.Exit(1)
	}
}

// run reads the tool spec and writes the lock file, or only checks it when check is set.
// With only set it regenerates just those tools and keeps the rest; a tool that fails keeps its previous entry when the version is unchanged.
func run(inPath, outPath, only string, check bool) error {
	data, err := os.ReadFile(inPath)
	if err != nil {
		return err
	}
	var s spec
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		return fmt.Errorf("%s: %w", inPath, err)
	}
	old := &tools.Lock{Tools: map[string]tools.Tool{}}
	if b, err := os.ReadFile(outPath); err == nil {
		if l, err := tools.ParseLock(b); err == nil {
			old = l
		}
	}
	if check {
		return checkLock(s, old)
	}

	want := map[string]bool{}
	for _, n := range strings.Split(only, ",") {
		if n = strings.TrimSpace(n); n != "" {
			want[n] = true
		}
	}
	ctx := context.Background()
	lock := tools.Lock{Tools: map[string]tools.Tool{}}
	names := make([]string, 0, len(s.Tools))
	for n := range s.Tools {
		names = append(names, n)
	}
	sort.Strings(names)
	var failed []string
	for _, name := range names {
		t := s.Tools[name]
		if len(want) > 0 && !want[name] {
			if prev, ok := old.Tools[name]; ok {
				lock.Tools[name] = prev
			}
			continue
		}
		fmt.Fprintf(os.Stderr, "%-14s %s (%s)\n", name, t.Version, t.Source)
		locked, err := lockTool(ctx, s.Platforms, name, t)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  error: %v\n", err)
			failed = append(failed, name)
			if prev, ok := old.Tools[name]; ok && prev.Version == t.Version {
				lock.Tools[name] = prev
			}
			continue
		}
		lock.Tools[name] = locked
	}
	b, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return err
	}
	if _, err := tools.ParseLock(b); err != nil {
		return err
	}
	if err := os.WriteFile(outPath, append(b, '\n'), 0o644); err != nil {
		return err
	}
	if len(failed) > 0 {
		return fmt.Errorf("could not lock: %s", strings.Join(failed, ", "))
	}
	return nil
}

// checkLock verifies every tool in the spec is locked at the same version
// and source, for every platform it needs.
func checkLock(s spec, l *tools.Lock) error {
	var errs []string
	for name, t := range s.Tools {
		got, ok := l.Tools[name]
		switch {
		case !ok:
			errs = append(errs, name+": not locked")
		case got.Version != t.Version || got.Source != t.Source:
			errs = append(errs, fmt.Sprintf("%s: lock has %s (%s), spec wants %s (%s)", name, got.Version, got.Source, t.Version, t.Source))
		case t.Source == tools.SourceArchive || t.Source == tools.SourcePyPI:
			for _, p := range s.Platforms {
				if _, ok := got.Platforms[p]; !ok {
					errs = append(errs, fmt.Sprintf("%s: no artifact for %s", name, p))
				}
			}
		}
	}
	for name := range l.Tools {
		if _, ok := s.Tools[name]; !ok {
			errs = append(errs, name+": locked but not in the spec")
		}
	}
	if len(errs) > 0 {
		sort.Strings(errs)
		return errors.New("lock is stale; run `go run ./internal/tools/lockgen`:\n  " + strings.Join(errs, "\n  "))
	}
	return nil
}

// lockTool builds the lock entry for one tool.
// Downloaded artifacts are hashed, and PyPI wheels are also checked against PyPI's published digest.
func lockTool(ctx context.Context, platforms []string, name string, t tool) (tools.Tool, error) {
	out := tools.Tool{Version: t.Version, Source: t.Source}
	switch t.Source {
	case tools.SourceArchive:
		out.Platforms = map[string]tools.Artifact{}
		for _, p := range platforms {
			a := tools.Artifact{URL: render(t.URL, t, p), Format: t.Format, Path: render(t.Path, t, p)}
			if a.Format == tools.FormatRaw {
				a.Path = ""
			}
			sum, err := fetchAndVerify(ctx, a)
			if err != nil {
				return out, fmt.Errorf("%s: %w", p, err)
			}
			a.SHA256 = sum
			out.Platforms[p] = a
		}
	case tools.SourcePyPI:
		files, err := pypiFiles(ctx, t.Package, t.Version)
		if err != nil {
			return out, err
		}
		out.Platforms = map[string]tools.Artifact{}
		for _, p := range platforms {
			tag, ok := t.Wheels[p]
			if !ok {
				return out, fmt.Errorf("no wheel tag for %s", p)
			}
			f, ok := pickWheel(files, tag)
			if !ok {
				return out, fmt.Errorf("%s: no wheel matching %q", p, tag)
			}
			a := tools.Artifact{URL: f.URL, Format: tools.FormatZip, Path: render(t.Path, t, p)}
			sum, err := fetchAndVerify(ctx, a)
			if err != nil {
				return out, fmt.Errorf("%s: %w", p, err)
			}
			if sum != f.Digests.SHA256 {
				return out, fmt.Errorf("%s: PyPI digest %s does not match download %s", p, f.Digests.SHA256, sum)
			}
			a.SHA256 = sum
			out.Platforms[p] = a
		}
	case tools.SourceUV:
		req, err := compileReqs(ctx, t.Package+"=="+t.Version)
		if err != nil {
			return out, err
		}
		out.Requirements = req
		out.Entrypoint = t.Entrypoint
		if out.Entrypoint == "" {
			out.Entrypoint = name
		}
	case tools.SourceGo:
		out.Module = t.Module
	default:
		return out, fmt.Errorf("unknown source %q", t.Source)
	}
	return out, nil
}

// render fills {version}, {os} and {arch} in tmpl for platform, applying the tool's OS and arch name overrides.
func render(tmpl string, t tool, platform string) string {
	goos, goarch, _ := strings.Cut(platform, "/")
	if v, ok := t.OS[goos]; ok {
		goos = v
	}
	if v, ok := t.Arch[goarch]; ok {
		goarch = v
	}
	return strings.NewReplacer("{version}", t.Version, "{os}", goos, "{arch}", goarch).Replace(tmpl)
}

var client = &http.Client{Timeout: 10 * time.Minute}

// pypiBase is the PyPI JSON API root; tests point it at a local server.
var pypiBase = "https://pypi.org"

// compileReqs produces hash-pinned requirements; tests replace it.
var compileReqs = compile

// fetchAndVerify downloads the artifact, hashes it, and checks that the
// executable can be extracted.
func fetchAndVerify(ctx context.Context, a tools.Artifact) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: %s", a.URL, resp.Status)
	}
	dir, err := os.MkdirTemp("", "lockgen-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	f, err := os.Create(filepath.Join(dir, "artifact"))
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", err
	}
	if err := tools.Extract(f.Name(), a.Format, a.Path, filepath.Join(dir, "bin")); err != nil {
		return "", fmt.Errorf("%s: %w", a.URL, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type pypiFile struct {
	Filename string `json:"filename"`
	URL      string `json:"url"`
	Digests  struct {
		SHA256 string `json:"sha256"`
	} `json:"digests"`
}

// pypiFiles lists the release files PyPI has for a package version.
func pypiFiles(ctx context.Context, pkg, version string) ([]pypiFile, error) {
	url := fmt.Sprintf("%s/pypi/%s/%s/json", pypiBase, pkg, version)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	var body struct {
		URLs []pypiFile `json:"urls"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	return body.URLs, nil
}

// pickWheel returns the first wheel file whose name contains tag.
func pickWheel(files []pypiFile, tag string) (pypiFile, bool) {
	for _, f := range files {
		if strings.HasSuffix(f.Filename, ".whl") && strings.Contains(f.Filename, tag) {
			return f, true
		}
	}
	return pypiFile{}, false
}

// compile produces a universal, hash-pinned requirements file.
func compile(ctx context.Context, requirement string) (string, error) {
	cmd := exec.CommandContext(ctx, "uv", "pip", "compile", "-", "--universal", "--generate-hashes",
		"--no-header", "--no-annotate", "--quiet", "--python-version", "3.10")
	cmd.Stdin = strings.NewReader(requirement + "\n")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("uv pip compile %s: %w: %s", requirement, err, stderr.String())
	}
	return string(out), nil
}
