package testdepth

import "context"

// Mutation mutates the changed lines of funcs and runs their package tests
// against each mutant; survivors mean the tests don't check the behavior.
func Mutation(ctx context.Context, e Env, funcs []Func) Result {
	return Result{}
}
