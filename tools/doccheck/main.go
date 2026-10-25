// Command doccheck fails when a function or method has no doc comment, or a
// package has no package comment. Test files and generated files are
// skipped.
//
// Usage: go run ./tools/doccheck ./...
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/rossedman/guardrails/tools/internal/gofiles"
)

// main runs the check on the patterns in the arguments.
func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// run reports every undocumented function and package under patterns to
// stdout and returns the exit code: 0 when there are none, 1 when there are,
// 2 when the files can't be read.
func run(patterns []string, stdout, stderr io.Writer) int {
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}
	missing := 0
	documented := map[string]bool{}
	err := gofiles.Walk(patterns, func(f gofiles.File) error {
		if f.Generated() {
			return nil
		}
		dir := filepath.Dir(f.Path)
		documented[dir] = documented[dir] || f.AST.Doc != nil
		for _, fd := range funcs(f) {
			fmt.Fprintf(stdout, "%s: %s has no doc comment\n", f.Path, fd)
			missing++
		}
		return nil
	})
	if err != nil {
		fmt.Fprintf(stderr, "doccheck: %v\n", err)
		return 2
	}
	dirs := make([]string, 0, len(documented))
	for d, ok := range documented {
		if !ok {
			dirs = append(dirs, d)
		}
	}
	sort.Strings(dirs)
	for _, d := range dirs {
		fmt.Fprintf(stdout, "%s: package has no package comment\n", d)
		missing++
	}
	if missing > 0 {
		fmt.Fprintf(stdout, "doccheck: %d missing\n", missing)
		return 1
	}
	return 0
}
