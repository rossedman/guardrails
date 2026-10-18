package feedback

import (
	"fmt"
	"strings"
	"testing"
)

func TestIssueLocation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		is   Issue
		want string
	}{
		{Issue{File: "a.go"}, "a.go"},
		{Issue{File: "a.go", Line: 3}, "a.go:3"},
		{Issue{File: "a.go", Line: 3, Col: 7}, "a.go:3:7"},
		{Issue{File: "a.go", Col: 7}, "a.go"},
	}
	for _, tc := range tests {
		if got := tc.is.Location(); got != tc.want {
			t.Errorf("Location(%+v) = %q, want %q", tc.is, got, tc.want)
		}
	}
}

func TestFingerprint(t *testing.T) {
	t.Parallel()
	a := Issue{File: "a.go", Line: 1, Rule: "errcheck", Message: "m"}
	b := a
	b.Col = 9 // column changes don't make it a new issue
	if a.Fingerprint() != b.Fingerprint() {
		t.Error("fingerprint should ignore column")
	}
	c := a
	c.Line = 2
	if a.Fingerprint() == c.Fingerprint() {
		t.Error("fingerprint should include line")
	}
}

func TestReportFailed(t *testing.T) {
	t.Parallel()
	r := Report{Checks: []Check{{Name: "a"}, {Name: "b", Failed: true}, {Name: "c", Failed: true}}}
	got := r.Failed()
	if len(got) != 2 || got[0].Name != "b" || got[1].Name != "c" {
		t.Errorf("Failed() = %+v", got)
	}
}

// TestRenderTemplate checks the exact format Claude parses.
func TestRenderTemplate(t *testing.T) {
	t.Parallel()
	r := Report{
		Title: "guardrails: go gate failed (2 of 4 checks)",
		Checks: []Check{
			{
				Name: "coverage", Failed: true, Summary: "84.2% < 90.0% (touched: ./internal/sched)",
				Lines: []string{"internal/sched/queue.go:41  (*Queue).Drain  0/14 stmts"},
				Fix:   "add table-driven tests for the functions above",
			},
			{Name: "build", Summary: "ok"},
			{
				Name: "lint", Failed: true, Summary: "2 issues",
				Issues: []Issue{
					{File: "internal/api/handler.go", Line: 102, Col: 5, Rule: "errcheck", Message: "error return of w.Write not checked"},
					{File: "x.go", Message: "no rule"},
				},
			},
		},
		Footer: "attempt 1 of 3",
	}
	got := Render(r, Options{})
	want := `guardrails: go gate failed (2 of 4 checks)

[coverage] 84.2% < 90.0% (touched: ./internal/sched)
  internal/sched/queue.go:41  (*Queue).Drain  0/14 stmts
  fix: add table-driven tests for the functions above

[lint] 2 issues
  internal/api/handler.go:102:5  errcheck  error return of w.Write not checked
  x.go  -  no rule

attempt 1 of 3
`
	if got.Text != want {
		t.Errorf("Text:\n%s\nwant:\n%s", got.Text, want)
	}
	if got.Full != want {
		t.Errorf("Full differs from Text for a small report:\n%s", got.Full)
	}
	if got.Truncated {
		t.Error("small report should not be truncated")
	}
	if len(got.New) != 2 {
		t.Errorf("New = %d fingerprints, want 2", len(got.New))
	}
}

func manyIssues(n int) []Issue {
	out := make([]Issue, n)
	for i := range out {
		out[i] = Issue{File: "f.go", Line: i + 1, Rule: "r", Message: fmt.Sprintf("issue %d", i)}
	}
	return out
}

func TestRenderCapsIssues(t *testing.T) {
	t.Parallel()
	r := Report{Title: "t", Checks: []Check{{Name: "lint", Failed: true, Summary: "s", Issues: manyIssues(13)}}}
	got := Render(r, Options{OverflowPath: "/data/s/report.txt"})
	if strings.Count(got.Text, "issue ") != MaxIssues {
		t.Errorf("shown %d issues, want %d:\n%s", strings.Count(got.Text, "issue "), MaxIssues, got.Text)
	}
	if !strings.Contains(got.Text, "… and 3 more") {
		t.Error("missing overflow count")
	}
	if !got.Truncated || !strings.HasSuffix(got.Text, "full report: /data/s/report.txt\n") {
		t.Errorf("missing pointer:\n%s", got.Text)
	}
	if strings.Count(got.Full, "issue ") != 13 {
		t.Error("Full should hold every issue")
	}
}

func TestRenderCapsLines(t *testing.T) {
	t.Parallel()
	lines := make([]string, 12)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i)
	}
	r := Report{Title: "t", Checks: []Check{{Name: "coverage", Failed: true, Lines: lines}}}
	got := Render(r, Options{MaxIssues: 5})
	if strings.Count(got.Text, "line ") != 5 || !strings.Contains(got.Text, "… and 7 more") {
		t.Errorf("lines not capped:\n%s", got.Text)
	}
	if !got.Truncated {
		t.Error("capped lines should mark truncated")
	}
	// No overflow path: no pointer line.
	if strings.Contains(got.Text, "full report:") {
		t.Error("unexpected pointer without overflow path")
	}
}

func TestRenderCapsChars(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", 300)
	var checks []Check
	for i := 0; i < 20; i++ {
		checks = append(checks, Check{Name: fmt.Sprintf("c%d", i), Failed: true, Summary: long})
	}
	got := Render(Report{Title: "t", Checks: checks}, Options{OverflowPath: "/p"})
	if len(got.Text) > MaxChars {
		t.Errorf("len(Text) = %d > %d", len(got.Text), MaxChars)
	}
	if !strings.Contains(got.Text, "… output truncated") || !strings.Contains(got.Text, "full report: /p") {
		t.Errorf("missing truncation markers:\n%s", got.Text[len(got.Text)-200:])
	}
	if len(got.Full) <= len(got.Text) {
		t.Error("Full should be longer than Text")
	}
}

func TestRenderDedup(t *testing.T) {
	t.Parallel()
	issues := manyIssues(3)
	seen := map[string]bool{issues[0].Fingerprint(): true, issues[1].Fingerprint(): true}
	got := Render(Report{Title: "t", Checks: []Check{{Name: "lint", Failed: true, Issues: issues}}}, Options{Seen: seen})
	if strings.Contains(got.Text, "issue 0") || strings.Contains(got.Text, "issue 1") {
		t.Errorf("seen issues repeated:\n%s", got.Text)
	}
	if !strings.Contains(got.Text, "issue 2") || !strings.Contains(got.Text, "(2 previously reported, unchanged)") {
		t.Errorf("dedup summary wrong:\n%s", got.Text)
	}
	if len(got.New) != 1 || got.New[0] != issues[2].Fingerprint() {
		t.Errorf("New = %v", got.New)
	}
	if !strings.Contains(got.Full, "issue 0") {
		t.Error("Full should include seen issues")
	}
}

func TestRenderNoFailures(t *testing.T) {
	t.Parallel()
	got := Render(Report{Title: "ok", Checks: []Check{{Name: "lint"}}}, Options{})
	if got.Text != "ok\n" {
		t.Errorf("Text = %q", got.Text)
	}
}

func TestCut(t *testing.T) {
	t.Parallel()
	tests := []struct {
		s    string
		n    int
		want string
	}{
		{"abc", 0, ""},
		{"abc", -1, ""},
		{"abc", 5, "abc"},
		{"ab\ncd\nef", 7, "ab\ncd\n"},
		{"abcdef", 3, "abc"},
	}
	for _, tc := range tests {
		if got := cut(tc.s, tc.n); got != tc.want {
			t.Errorf("cut(%q, %d) = %q, want %q", tc.s, tc.n, got, tc.want)
		}
	}
}
