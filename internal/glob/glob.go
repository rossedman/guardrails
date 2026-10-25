// Package glob matches slash-separated paths against patterns that support
// "**" for any number of directories, on top of path.Match syntax.
package glob

import (
	"path"
	"strings"
)

// Match reports whether name matches pattern. Both use forward slashes.
// "**" as a whole segment matches zero or more segments; every other
// segment follows path.Match. An invalid pattern never matches.
func Match(pattern, name string) bool {
	return match(split(pattern), split(name))
}

// Any reports whether name matches any of patterns.
func Any(patterns []string, name string) bool {
	for _, p := range patterns {
		if Match(p, name) {
			return true
		}
	}
	return false
}

// split breaks s into its slash-separated segments, ignoring leading and trailing slashes.
func split(s string) []string {
	s = strings.Trim(s, "/")
	if s == "" {
		return nil
	}
	return strings.Split(s, "/")
}

// match reports whether the name segments match the pattern segments.
// Each segment uses path.Match rules, and "**" matches any number of segments.
func match(pat, name []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			rest := pat[1:]
			for i := 0; i <= len(name); i++ {
				if match(rest, name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		ok, err := path.Match(pat[0], name[0])
		if err != nil || !ok {
			return false
		}
		pat, name = pat[1:], name[1:]
	}
	return len(name) == 0
}
