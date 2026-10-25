package deps

import (
	_ "embed"
	"strings"
)

// The most-downloaded npm and PyPI packages, most popular first, from
// ./popular/gen.
var (
	//go:embed popular/npm.txt
	npmPopular string
	//go:embed popular/pypi.txt
	pypiPopular string
)

// popularList is a ranked list of names with a set for membership.
type popularList struct {
	names []string
	set   map[string]bool
}

// newPopular builds a popularList from whitespace-separated package names.
func newPopular(text string) popularList {
	p := popularList{set: map[string]bool{}}
	for _, n := range strings.Fields(text) {
		p.names = append(p.names, n)
		p.set[n] = true
	}
	return p
}

var popular = map[string]popularList{
	NPM:  newPopular(npmPopular),
	PyPI: newPopular(pypiPopular),
}

// minTypoLen is the shortest name checked for one-edit near misses; short
// names are too close to each other for that to mean anything.
const minTypoLen = 5

// Suggest returns the popular package a name looks like a typo of, or ""
// when the name is popular itself, or not close to one. Close means one
// insertion, deletion, substitution or transposition away, or the same
// once case and separators are ignored. Only npm and PyPI have lists.
func Suggest(system, name string) string {
	list, ok := popular[system]
	if !ok || list.set[name] {
		return ""
	}
	norm := squash(name)
	for _, p := range list.names {
		if squash(p) == norm {
			return p
		}
	}
	if len(name) < minTypoLen {
		return ""
	}
	for _, p := range list.names {
		if oneEdit(name, p) {
			return p
		}
	}
	return ""
}

// squash lowercases a name and drops separators, so react-dom, ReactDOM
// and react_dom compare equal.
func squash(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '-', '_', '.':
			return -1
		}
		return r
	}, strings.ToLower(s))
}

// oneEdit reports whether a and b differ by exactly one insertion,
// deletion, substitution or adjacent transposition.
func oneEdit(a, b string) bool {
	if a == b {
		return false
	}
	la, lb := len(a), len(b)
	if la-lb > 1 || lb-la > 1 {
		return false
	}
	i := 0
	for i < la && i < lb && a[i] == b[i] {
		i++
	}
	switch {
	case la == lb:
		if a[i+1:] == b[i+1:] {
			return true // substitution
		}
		return i+1 < la && a[i] == b[i+1] && a[i+1] == b[i] && a[i+2:] == b[i+2:]
	case la > lb:
		return a[i+1:] == b[i:]
	default:
		return a[i:] == b[i+1:]
	}
}
