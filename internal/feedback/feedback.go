// Package feedback renders check results into the one message format Claude
// sees for every blocking decision.
//
// The format is file:line first, one fix hint per group, at most MaxIssues
// issues per check and MaxChars characters overall; anything beyond that is
// written to a scratchpad file with a pointer, staying well under Claude
// Code's 10,000-character hook output limit.
package feedback

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rossedman/guardrails/internal/state"
)

// Default caps.
const (
	MaxIssues = 10
	MaxChars  = 4000
)

// Issue is one finding at a source location.
type Issue struct {
	File    string `json:"file"`
	Line    int    `json:"line,omitempty"`
	Col     int    `json:"col,omitempty"`
	Rule    string `json:"rule,omitempty"`
	Message string `json:"message"`
}

// Location renders file:line:col, omitting zero parts.
func (i Issue) Location() string {
	loc := i.File
	if i.Line > 0 {
		loc += fmt.Sprintf(":%d", i.Line)
		if i.Col > 0 {
			loc += fmt.Sprintf(":%d", i.Col)
		}
	}
	return loc
}

// Fingerprint identifies an issue for dedup across a session.
func (i Issue) Fingerprint() string {
	return state.Key(i.File, fmt.Sprint(i.Line), i.Rule, i.Message)
}

// Check is the result of one named check.
type Check struct {
	// Name is the bracketed label, e.g. "lint" or "coverage".
	Name string `json:"name"`
	// Summary follows the label on the header line.
	Summary string `json:"summary"`
	// Lines are preformatted detail lines (e.g. uncovered functions).
	Lines []string `json:"lines,omitempty"`
	// Issues are located findings.
	Issues []Issue `json:"issues,omitempty"`
	// Fix is the one-line hint for this group.
	Fix string `json:"fix,omitempty"`
	// Failed marks the check as failing; passing checks are not rendered.
	Failed bool `json:"failed"`
}

// Report is a full message for Claude.
type Report struct {
	Title  string  `json:"title"`
	Checks []Check `json:"checks"`
	Footer string  `json:"footer,omitempty"`
}

// Failed returns the failing checks.
func (r Report) Failed() []Check {
	var out []Check
	for _, c := range r.Checks {
		if c.Failed {
			out = append(out, c)
		}
	}
	return out
}

// Options control rendering.
type Options struct {
	MaxIssues int
	MaxChars  int
	// Seen holds fingerprints already reported this session. Matching
	// issues are summarized as a count instead of repeated.
	Seen map[string]bool
	// OverflowPath is where the full report would be written; it is named
	// in the truncation pointer.
	OverflowPath string
}

// Rendered is the output of Render.
type Rendered struct {
	// Text is the capped message for Claude.
	Text string
	// Full is the uncapped message, to be written to OverflowPath when
	// Truncated is set.
	Full      string
	Truncated bool
	// New holds fingerprints of issues shown for the first time.
	New []string
}

// Render formats the failing checks of r.
func Render(r Report, opts Options) Rendered {
	if opts.MaxIssues <= 0 {
		opts.MaxIssues = MaxIssues
	}
	if opts.MaxChars <= 0 {
		opts.MaxChars = MaxChars
	}
	var (
		short, full strings.Builder
		out         Rendered
		capped      bool
	)
	short.WriteString(r.Title + "\n")
	full.WriteString(r.Title + "\n")

	for _, c := range r.Failed() {
		header := fmt.Sprintf("\n[%s] %s\n", c.Name, c.Summary)
		short.WriteString(header)
		full.WriteString(header)
		for _, l := range c.Lines {
			full.WriteString("  " + l + "\n")
		}
		for i, l := range c.Lines {
			if i == opts.MaxIssues {
				fmt.Fprintf(&short, "  … and %d more\n", len(c.Lines)-opts.MaxIssues)
				capped = true
				break
			}
			short.WriteString("  " + l + "\n")
		}

		shown, repeated := 0, 0
		for _, is := range c.Issues {
			line := fmt.Sprintf("  %s  %s  %s\n", is.Location(), orDash(is.Rule), is.Message)
			full.WriteString(line)
			fp := is.Fingerprint()
			if opts.Seen[fp] {
				repeated++
				continue
			}
			out.New = append(out.New, fp)
			if shown < opts.MaxIssues {
				short.WriteString(line)
			}
			shown++
		}
		if shown > opts.MaxIssues {
			fmt.Fprintf(&short, "  … and %d more\n", shown-opts.MaxIssues)
			capped = true
		}
		if repeated > 0 {
			fmt.Fprintf(&short, "  (%d previously reported, unchanged)\n", repeated)
		}
		if c.Fix != "" {
			fix := "  fix: " + c.Fix + "\n"
			short.WriteString(fix)
			full.WriteString(fix)
		}
	}
	if r.Footer != "" {
		short.WriteString("\n" + r.Footer + "\n")
		full.WriteString("\n" + r.Footer + "\n")
	}

	out.Full = full.String()
	out.Text = short.String()
	if len(out.Text) > opts.MaxChars {
		pointer := "\n… output truncated"
		budget := opts.MaxChars - len(pointer) - len(opts.OverflowPath) - 20
		out.Text = cut(out.Text, budget) + pointer
		capped = true
	}
	if capped {
		out.Truncated = true
		if opts.OverflowPath != "" {
			out.Text = strings.TrimRight(out.Text, "\n") + "\nfull report: " + filepath.ToSlash(opts.OverflowPath) + "\n"
		}
	}
	return out
}

// orDash returns s, or "-" if it is empty.
func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// cut truncates s to at most n bytes at a line boundary when possible.
func cut(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	s = s[:n]
	if i := strings.LastIndexByte(s, '\n'); i > 0 {
		return s[:i+1]
	}
	return s
}
