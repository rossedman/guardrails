// Package tools provisions the external tools Guardrails runs, so a host
// needs little more than the language runtime it already codes in.
//
// Every tool is pinned in tools.yaml and locked in tools.lock.json, which
// records a URL and SHA-256 per platform. Resolution prefers what is already
// installed (PATH, or the managed copy, per config), and otherwise installs
// the pinned version into the plugin data directory after verifying its
// checksum.
package tools

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"runtime"
	"sort"
)

// Source kinds.
const (
	SourceArchive = "archive"
	SourcePyPI    = "pypi"
	SourceUV      = "uv"
	SourceGo      = "go"
)

// Archive formats.
const (
	FormatTarGz = "tar.gz"
	FormatTarXz = "tar.xz"
	FormatZip   = "zip"
	FormatRaw   = "raw"
)

//go:embed tools.lock.json
var lockJSON []byte

// Lock is the generated, checksummed manifest.
type Lock struct {
	Tools map[string]Tool `json:"tools"`
}

// Tool is one locked tool.
type Tool struct {
	Version string `json:"version"`
	Source  string `json:"source"`
	// Platforms holds the per-platform artifact for archive and pypi
	// sources, keyed by "os/arch".
	Platforms map[string]Artifact `json:"platforms,omitempty"`
	// Requirements is a hash-pinned requirements file for uv sources.
	Requirements string `json:"requirements,omitempty"`
	// Entrypoint is the executable name for uv sources.
	Entrypoint string `json:"entrypoint,omitempty"`
	// Module is the `go install` target for go sources.
	Module string `json:"module,omitempty"`
}

// Artifact is a downloadable file and the executable inside it.
type Artifact struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Format string `json:"format"`
	// Path is the executable's path inside the archive; empty for raw.
	Path string `json:"path,omitempty"`
}

// DefaultLock returns the lock embedded in the binary.
func DefaultLock() (*Lock, error) {
	return ParseLock(lockJSON)
}

// ParseLock decodes and validates a lock.
func ParseLock(data []byte) (*Lock, error) {
	var l Lock
	if err := json.Unmarshal(data, &l); err != nil {
		return nil, fmt.Errorf("parse tools lock: %w", err)
	}
	if l.Tools == nil {
		l.Tools = map[string]Tool{}
	}
	for name, t := range l.Tools {
		if err := t.validate(); err != nil {
			return nil, fmt.Errorf("tools lock: %s: %w", name, err)
		}
	}
	return &l, nil
}

// Names returns the locked tool names, sorted.
func (l *Lock) Names() []string {
	out := make([]string, 0, len(l.Tools))
	for n := range l.Tools {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// validate checks that the tool has a version and what its source needs: platform artifacts with a URL and SHA-256, requirements and an entrypoint, or a module.
func (t Tool) validate() error {
	if t.Version == "" {
		return fmt.Errorf("missing version")
	}
	switch t.Source {
	case SourceArchive, SourcePyPI:
		for p, a := range t.Platforms {
			if a.URL == "" || len(a.SHA256) != 64 {
				return fmt.Errorf("platform %s: missing url or sha256", p)
			}
			switch a.Format {
			case FormatTarGz, FormatTarXz, FormatZip:
				if a.Path == "" {
					return fmt.Errorf("platform %s: archive needs a path", p)
				}
			case FormatRaw:
			default:
				return fmt.Errorf("platform %s: unknown format %q", p, a.Format)
			}
		}
	case SourceUV:
		if t.Requirements == "" || t.Entrypoint == "" {
			return fmt.Errorf("uv source needs requirements and entrypoint")
		}
	case SourceGo:
		if t.Module == "" {
			return fmt.Errorf("go source needs a module")
		}
	default:
		return fmt.Errorf("unknown source %q", t.Source)
	}
	return nil
}

// Platform returns the current "os/arch" key.
func Platform() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}
