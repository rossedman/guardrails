package testdepth

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rossedman/guardrails/internal/feedback"
)

// fuzzRunSlack is added to -fuzztime for compiling and minimizing.
const fuzzRunSlack = 15 * time.Second

// Fuzz requires fuzz targets for changed functions that parse untrusted
// input and runs touched targets briefly.
func Fuzz(ctx context.Context, e Env, funcs []Func, touched []string) Result {
	var res Result
	cfg := e.Config.Go.Fuzz
	idx := fuzzIndex{}
	if cfg.RequireOnParsers && len(funcs) > 0 {
		res.Ran = append(res.Ran, "fuzz-targets")
		c, asked, ok := fuzzRequirements(e, funcs, idx)
		if ok {
			res.Checks = append(res.Checks, c)
		}
		res.Asked = append(res.Asked, asked...)
	}
	targets := fuzzTargets(funcs, touched, idx)
	if len(targets) == 0 {
		return res
	}
	res.Merge(fuzzRun(ctx, e, targets))
	return res
}

// fuzzIndex caches IndexTests per package directory.
type fuzzIndex map[string]*Tests

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

// fuzzRequirements returns the failing "fuzz-targets" check, the keys of
// borderline requirements raised now, and whether there is a check.
func fuzzRequirements(e Env, funcs []Func, idx fuzzIndex) (feedback.Check, []string, bool) {
	var (
		lines []string
		asked []string
	)
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
		line := fmt.Sprintf("%s:%d  %s  needs a Fuzz target (%s)", fuzzRel(e.Repo, f.File), f.Start, f.Name, fuzzReason(sig, f))
		if !sig.Strong {
			key := "fuzz:" + f.Key(e.Root)
			if e.Asked[key] {
				continue
			}
			asked = append(asked, key)
			line += " — or say in one line why not"
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return feedback.Check{}, asked, false
	}
	summary := fmt.Sprintf("%d functions that parse untrusted input have no fuzz target", len(lines))
	if len(lines) == 1 {
		summary = "1 function that parses untrusted input has no fuzz target"
	}
	return feedback.Check{
		Name:    "fuzz-targets",
		Failed:  true,
		Summary: summary,
		Lines:   lines,
		Fix:     "add Fuzz functions with a seed corpus (f.Add) that call these and assert invariants such as round-trips; the human can opt out with // guardrails:nofuzz <reason>",
	}, asked, true
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
