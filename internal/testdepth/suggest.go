package testdepth

import "fmt"

// BenchSuggestions lists changed hot-path functions with no benchmark
// calling them. perfPackages are module-relative patterns of
// performance-critical packages.
func BenchSuggestions(e Env, funcs []Func, perfPackages []string) []string {
	idx := fuzzIndex{}
	var lines []string
	for _, f := range funcs {
		if _, ok := f.Annotations["nobench"]; ok {
			continue
		}
		sig := HotPath(f, e.Root, perfPackages)
		if !sig.Any() {
			continue
		}
		if t := idx.get(f.Dir); t != nil && len(Covering(t.Benchmarks, f)) > 0 {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s:%d  %s  could use a Benchmark (%s)", fuzzRel(e.Repo, f.File), f.Start, f.Name, fuzzReason(sig, f)))
	}
	return lines
}
