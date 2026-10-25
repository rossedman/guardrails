package feedback

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func benchReport(issues int) Report {
	c := Check{Name: "lint", Summary: fmt.Sprintf("%d issues", issues), Fix: "run golangci-lint run --fix", Failed: true}
	for i := range issues {
		c.Issues = append(c.Issues, Issue{File: fmt.Sprintf("internal/pkg/f%d.go", i%7), Line: i + 1, Col: 3, Rule: "errcheck", Message: "error return value not checked"})
	}
	cov := Check{Name: "coverage", Summary: "total 71.0% < 90%", Failed: true}
	for i := range 30 {
		cov.Lines = append(cov.Lines, fmt.Sprintf("internal/pkg/f.go:%d  Func%d 0.0%%", i+1, i))
	}
	return Report{Title: "guardrails: 2 checks failed", Checks: []Check{c, cov, {Name: "vet", Failed: false}}, Footer: "fix these, then stop again"}
}

func BenchmarkRender(b *testing.B) {
	for _, n := range []int{3, 200} {
		r := benchReport(n)
		seen := map[string]bool{r.Checks[0].Issues[0].Fingerprint(): true}
		b.Run(fmt.Sprintf("issues=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = Render(r, Options{Seen: seen, OverflowPath: "/tmp/guardrails/overflow.txt"})
			}
		})
	}
}

func FuzzRender(f *testing.F) {
	f.Add("guardrails", "lint", "3 issues", "a.go", "unused variable", "fix it", 3, 5, 400)
	f.Add("t", "coverage", "low", "b/c.go", "multi\nline\nmessage", "", 40, 2, 200)
	f.Add("", "", "", "", "", "", 0, 0, 0)
	f.Fuzz(func(t *testing.T, title, name, summary, file, msg, fix string, nIssues, maxIssues, maxChars int) {
		nIssues = max(0, min(nIssues, 60))
		maxIssues = max(0, min(maxIssues, 20))
		maxChars = 200 + max(0, min(maxChars, 3000)) // leaves room for the truncation pointer
		c := Check{Name: name, Summary: summary, Fix: fix, Failed: true}
		for i := range nIssues {
			c.Issues = append(c.Issues, Issue{File: file, Line: i, Message: msg})
			c.Lines = append(c.Lines, msg)
		}
		r := Report{Title: title, Checks: []Check{c, {Name: "passing", Summary: "ok-" + summary}}}
		opts := Options{MaxIssues: maxIssues, MaxChars: maxChars, OverflowPath: "/tmp/o.txt"}

		got := Render(r, opts)
		if again := Render(r, opts); got.Text != again.Text || got.Full != again.Full || got.Truncated != again.Truncated {
			t.Fatal("Render is not deterministic")
		}
		if len(got.Text) > maxChars {
			t.Fatalf("Text is %d bytes, cap is %d", len(got.Text), maxChars)
		}
		if !got.Truncated && got.Text != got.Full {
			t.Fatalf("not truncated but Text differs from Full:\n%q\n%q", got.Text, got.Full)
		}
		if got.Truncated && !strings.Contains(got.Text, "full report: /tmp/o.txt") {
			t.Fatalf("truncated output lacks the overflow pointer: %q", got.Text)
		}
		if len(got.New) != nIssues {
			t.Fatalf("New has %d fingerprints for %d unseen issues", len(got.New), nIssues)
		}

		// Everything seen is summarized, not repeated, and not reported as new.
		seen := map[string]bool{}
		for _, fp := range got.New {
			seen[fp] = true
		}
		opts.Seen = seen
		again := Render(r, opts)
		if len(again.New) != 0 {
			t.Fatalf("seen issues reported as new again: %d", len(again.New))
		}
		if len(again.Text) > maxChars {
			t.Fatalf("Text is %d bytes with Seen, cap is %d", len(again.Text), maxChars)
		}

		valid := true
		for _, s := range []string{title, name, summary, file, msg, fix} {
			valid = valid && utf8.ValidString(s)
		}
		if valid && !utf8.ValidString(got.Text) {
			t.Fatalf("valid input produced invalid UTF-8: %q", got.Text)
		}
	})
}
