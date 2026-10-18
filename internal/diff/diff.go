// Package diff extracts changed line numbers from unified diffs.
package diff

import (
	"bufio"
	"strconv"
	"strings"
)

// Changed maps a file path (as written in the diff, relative to the repo
// root) to the set of line numbers added or modified in the new version.
type Changed map[string]map[int]bool

// ParseUnified parses `git diff --unified=0` output. Deleted files are
// skipped; only lines present in the new version are recorded.
func ParseUnified(text string) Changed {
	out := Changed{}
	var (
		file string
		line int
		prev string
	)
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		l := sc.Text()
		// A file header "+++ " always follows a "--- " line; inside a hunk
		// the same text is an added line whose content starts with "++ ".
		header := strings.HasPrefix(l, "+++ ") && (strings.HasPrefix(prev, "--- ") || file == "")
		prev = l
		switch {
		case header:
			file = strings.TrimPrefix(strings.TrimPrefix(l, "+++ "), "b/")
			if file == "/dev/null" {
				file = ""
			}
		case strings.HasPrefix(l, "@@"):
			line = hunkStart(l)
		case strings.HasPrefix(l, "+") && file != "":
			if out[file] == nil {
				out[file] = map[int]bool{}
			}
			out[file][line] = true
			line++
		case strings.HasPrefix(l, " ") && file != "":
			line++
		}
	}
	return out
}

// hunkStart returns the new-file start line from "@@ -a,b +c,d @@".
func hunkStart(h string) int {
	plus := strings.Index(h, "+")
	if plus < 0 {
		return 0
	}
	rest := h[plus+1:]
	end := strings.IndexAny(rest, ", ")
	if end >= 0 {
		rest = rest[:end]
	}
	n, err := strconv.Atoi(rest)
	if err != nil {
		return 0
	}
	return n
}
