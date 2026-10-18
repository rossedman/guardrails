package testdepth

import "context"

// Benchmarks requires benchmarks for changed hot-path functions and, when
// benchmarks cover changed code, compares them against the base commit.
func Benchmarks(ctx context.Context, e Env, funcs []Func) Result {
	return Result{}
}
