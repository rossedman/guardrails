// Command gendocs writes one Markdown reference page per guardrails command
// into docs/reference/commands, so the docs never drift from the CLI.
//
// Usage: go run ./tools/gendocs [dir]
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"

	"github.com/rossedman/guardrails/internal/cli"
)

// main writes the pages into the directory in the arguments, or the default.
func main() { os.Exit(run(os.Args[1:], os.Stderr)) }

// run regenerates the reference pages in args[0] (default
// docs/reference/commands), removing stale ones, and returns the exit code.
func run(args []string, stderr io.Writer) int {
	dir := filepath.Join("docs", "reference", "commands")
	if len(args) > 0 {
		dir = args[0]
	}
	if err := generate(cli.New().Command(), dir); err != nil {
		fmt.Fprintf(stderr, "gendocs: %v\n", err)
		return 1
	}
	return 0
}

// generate replaces the Markdown pages in dir with one per visible command
// of root, linked to each other by relative paths.
func generate(root *cobra.Command, dir string) error {
	old, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil {
		return err
	}
	for _, f := range old {
		if err := os.Remove(f); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	root.DisableAutoGenTag = true
	if err := doc.GenMarkdownTreeCustom(root, dir, func(string) string { return "" }, link); err != nil {
		return err
	}
	pages, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil {
		return err
	}
	for _, page := range pages {
		if err := promoteTitle(page); err != nil {
			return err
		}
	}
	return nil
}

// promoteTitle makes the "## guardrails gate" line cobra starts each page
// with the page's H1, so MkDocs takes it as the title instead of adding its
// own above it.
func promoteTitle(page string) error {
	b, err := os.ReadFile(page)
	if err != nil {
		return err
	}
	if rest, ok := strings.CutPrefix(string(b), "## "); ok {
		b = []byte("# " + rest)
	}
	return os.WriteFile(page, b, 0o644)
}

// link turns a generated file name into the page link: the same file name,
// since every page sits in one directory.
func link(name string) string { return strings.ToLower(name) }
