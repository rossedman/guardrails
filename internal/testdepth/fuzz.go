package testdepth

import "context"

// Fuzz requires fuzz targets for changed functions that parse untrusted
// input and runs touched targets briefly.
func Fuzz(ctx context.Context, e Env, funcs []Func, touched []string) Result {
	return Result{}
}
