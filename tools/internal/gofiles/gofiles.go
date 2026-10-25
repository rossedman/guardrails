// Package gofiles walks a module's Go source files for the repo checks in
// tools/.
package gofiles

import (
	"bufio"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// File is a parsed, non-test Go source file.
type File struct {
	// Path is the file's path, relative to the walk's root when the pattern
	// was relative.
	Path string
	// AST is the parsed file, with comments.
	AST *ast.File
}

// Generated reports whether the file carries the standard
// "// Code generated ... DO NOT EDIT." header.
func (f File) Generated() bool { return ast.IsGenerated(f.AST) }

// Walk parses every non-test .go file matched by patterns and calls fn for
// each, in path order. A pattern is a directory, which matches its own
// files, or a directory followed by "/...", which also matches every
// directory below it except testdata, vendor and hidden ones.
func Walk(patterns []string, fn func(File) error) error {
	paths := map[string]bool{}
	for _, pat := range patterns {
		dir, recursive := strings.CutSuffix(pat, "/...")
		if dir == "..." {
			dir, recursive = ".", true
		}
		err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if p != dir && (!recursive || skipDir(d.Name())) {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
				paths[p] = true
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	sorted := make([]string, 0, len(paths))
	for p := range paths {
		sorted = append(sorted, p)
	}
	sort.Strings(sorted)
	fset := token.NewFileSet()
	for _, p := range sorted {
		f, err := parser.ParseFile(fset, p, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		if err := fn(File{Path: p, AST: f}); err != nil {
			return err
		}
	}
	return nil
}

// skipDir reports whether a directory below a "/..." pattern is left out.
func skipDir(name string) bool {
	return name == "testdata" || name == "vendor" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

// ModulePath returns the module path declared in dir/go.mod.
func ModulePath(dir string) (string, error) {
	f, err := os.Open(filepath.Join(dir, "go.mod"))
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if mod, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "module "); ok {
			return strings.Trim(strings.TrimSpace(mod), `"`), nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", errors.New("go.mod has no module line")
}
