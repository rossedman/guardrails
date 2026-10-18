// Package coverage parses Go cover profiles and answers the questions the
// Stop gate asks: total and per-package coverage, which functions hold the
// most uncovered statements, and how much of a diff is covered.
package coverage

import (
	"bufio"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
)

// Block is one coverage block from a profile.
type Block struct {
	// File is the import-path-qualified file name, e.g.
	// github.com/acme/app/internal/api/handler.go.
	File      string
	StartLine int
	StartCol  int
	EndLine   int
	EndCol    int
	NumStmt   int
	Count     int
}

// Covered reports whether the block ran at least once.
func (b Block) Covered() bool { return b.Count > 0 }

// Profile is a parsed cover profile.
type Profile struct {
	Mode   string
	Blocks []Block
}

// Parse reads a profile produced by go test -coverprofile. Blocks that
// appear more than once (common with -coverpkg) are merged, keeping the
// highest count.
func Parse(r io.Reader) (*Profile, error) {
	p := &Profile{}
	seen := map[string]int{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "mode:") {
			p.Mode = strings.TrimSpace(strings.TrimPrefix(line, "mode:"))
			continue
		}
		b, err := parseLine(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNo, err)
		}
		key := fmt.Sprintf("%s:%d.%d,%d.%d", b.File, b.StartLine, b.StartCol, b.EndLine, b.EndCol)
		if i, ok := seen[key]; ok {
			if b.Count > p.Blocks[i].Count {
				p.Blocks[i].Count = b.Count
			}
			continue
		}
		seen[key] = len(p.Blocks)
		p.Blocks = append(p.Blocks, b)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return p, nil
}

// parseLine parses "file.go:10.2,12.3 2 1".
func parseLine(line string) (Block, error) {
	var b Block
	colon := strings.LastIndex(line, ":")
	if colon < 0 {
		return b, fmt.Errorf("malformed block %q", line)
	}
	b.File = line[:colon]
	fields := strings.Fields(line[colon+1:])
	if len(fields) != 3 {
		return b, fmt.Errorf("malformed block %q", line)
	}
	n, err := fmt.Sscanf(fields[0], "%d.%d,%d.%d", &b.StartLine, &b.StartCol, &b.EndLine, &b.EndCol)
	if err != nil || n != 4 {
		return b, fmt.Errorf("malformed range in %q", line)
	}
	if b.NumStmt, err = strconv.Atoi(fields[1]); err != nil {
		return b, fmt.Errorf("malformed statement count in %q", line)
	}
	if b.Count, err = strconv.Atoi(fields[2]); err != nil {
		return b, fmt.Errorf("malformed hit count in %q", line)
	}
	return b, nil
}

// Filter returns a profile without blocks whose file keep rejects.
func (p *Profile) Filter(keep func(file string) bool) *Profile {
	out := &Profile{Mode: p.Mode}
	for _, b := range p.Blocks {
		if keep(b.File) {
			out.Blocks = append(out.Blocks, b)
		}
	}
	return out
}

// Stats is a covered/total statement tally.
type Stats struct {
	Covered int
	Total   int
}

// Percent returns coverage as 0–100. Empty stats count as fully covered,
// since there is nothing to test.
func (s Stats) Percent() float64 {
	if s.Total == 0 {
		return 100
	}
	return float64(s.Covered) * 100 / float64(s.Total)
}

func (s *Stats) add(b Block) {
	s.Total += b.NumStmt
	if b.Covered() {
		s.Covered += b.NumStmt
	}
}

// Total returns statement coverage across the whole profile.
func (p *Profile) Total() Stats {
	var s Stats
	for _, b := range p.Blocks {
		s.add(b)
	}
	return s
}

// ByPackage returns statement coverage keyed by package import path.
func (p *Profile) ByPackage() map[string]Stats {
	out := map[string]Stats{}
	for _, b := range p.Blocks {
		pkg := path.Dir(b.File)
		s := out[pkg]
		s.add(b)
		out[pkg] = s
	}
	return out
}

// Packages returns the sorted package import paths in the profile.
func (p *Profile) Packages() []string {
	m := p.ByPackage()
	out := make([]string, 0, len(m))
	for pkg := range m {
		out = append(out, pkg)
	}
	sort.Strings(out)
	return out
}
