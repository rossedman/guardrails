package testdepth

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// fuzzRunSlack is added to -fuzztime for compiling and minimizing.
const fuzzRunSlack = 15 * time.Second

// Fuzz briefly runs the fuzz targets that cover changed functions or are
// declared in touched test files.
func Fuzz(ctx context.Context, e Env, funcs []Func, touched []string) Result {
	targets := fuzzTargets(funcs, touched, fuzzIndex{})
	if len(targets) == 0 {
		return Result{}
	}
	return fuzzRun(ctx, e, targets)
}

// fuzzIndex caches IndexTests per package directory.
type fuzzIndex map[string]*Tests

// get returns the indexed tests for dir, indexing it on first use. It returns nil if indexing fails.
func (x fuzzIndex) get(dir string) *Tests {
	if t, ok := x[dir]; ok {
		return t
	}
	t, err := IndexTests(dir)
	if err != nil {
		t = nil
	}
	x[dir] = t
	return t
}

// FuzzSuggestions lists changed functions that parse loosely structured
// input but have no fuzz target calling them.
func FuzzSuggestions(e Env, funcs []Func) []string {
	idx := fuzzIndex{}
	var lines []string
	for _, f := range funcs {
		if _, ok := f.Annotations["nofuzz"]; ok {
			continue
		}
		sig := Parser(f)
		if !sig.Any() {
			continue
		}
		if t := idx.get(f.Dir); t != nil && len(Covering(t.Fuzz, f)) > 0 {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s:%d  %s  could use a Fuzz target (%s)", fuzzRel(e.Repo, f.File), f.Start, f.Name, fuzzReason(sig, f)))
	}
	return lines
}

// fuzzReason renders the first signal without repeating the function name.
func fuzzReason(sig Signals, f Func) string {
	r := sig.Reasons[0]
	return strings.TrimPrefix(r, f.Ident+" ")
}

// fuzzTarget is one fuzz function in a package directory.
type fuzzTarget struct {
	Dir  string
	Name string
}

// fuzzTargets selects fuzz targets covering changed functions and those
// declared in touched test files, deduplicated and sorted.
func fuzzTargets(funcs []Func, touched []string, idx fuzzIndex) []fuzzTarget {
	seen := map[fuzzTarget]bool{}
	var out []fuzzTarget
	add := func(t fuzzTarget) {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	for _, f := range funcs {
		if t := idx.get(f.Dir); t != nil {
			for _, name := range Covering(t.Fuzz, f) {
				add(fuzzTarget{Dir: f.Dir, Name: name})
			}
		}
	}
	for _, path := range touched {
		if !strings.HasSuffix(path, "_test.go") {
			continue
		}
		dir := filepath.Dir(path)
		t := idx.get(dir)
		if t == nil {
			continue
		}
		for name := range t.Fuzz {
			if filepath.Clean(t.Files[name]) == filepath.Clean(path) {
				add(fuzzTarget{Dir: dir, Name: name})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir < out[j].Dir
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// fuzzRel renders p relative to the repo, with forward slashes.
func fuzzRel(repo, p string) string {
	if rel, err := filepath.Rel(repo, p); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(p)
}
