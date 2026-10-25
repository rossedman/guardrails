// Command generated prints the import-path-qualified names of generated Go
// files (those with the standard "Code generated ... DO NOT EDIT." header),
// one per line, so the coverage gate can leave them out.
//
// Usage: go run ./tools/generated ./...
package main

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"

	"github.com/rossedman/guardrails/tools/internal/gofiles"
)

// main lists the generated files under the patterns in the arguments.
func main() { os.Exit(run(".", os.Args[1:], os.Stdout, os.Stderr)) }

// run prints each generated file under patterns as <module>/<path>, with
// paths taken relative to root, which holds go.mod. It returns 0, or 2 when
// the module or its files can't be read.
func run(root string, patterns []string, stdout, stderr io.Writer) int {
	mod, err := gofiles.ModulePath(root)
	if err != nil {
		fmt.Fprintf(stderr, "generated: %v\n", err)
		return 2
	}
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}
	err = gofiles.Walk(patterns, func(f gofiles.File) error {
		if !f.Generated() {
			return nil
		}
		rel, err := filepath.Rel(root, f.Path)
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, path.Join(mod, filepath.ToSlash(rel)))
		return nil
	})
	if err != nil {
		fmt.Fprintf(stderr, "generated: %v\n", err)
		return 2
	}
	return 0
}
