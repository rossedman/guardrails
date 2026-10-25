package lessons

import (
	"slices"
	"strings"
	"testing"
)

func BenchmarkDetect(b *testing.B) {
	prompts := []string{
		"No, use the existing helper in internal/util. We don't use testify in this codebase, and always run the race detector.",
		"Add a --json flag to the insights command and update the docs so the new output format is covered by a golden test.",
		"Fix this:\n```\n" + strings.Repeat("goroutine 1 [running]:\nmain.main()\n", 40) + "```\nwhy did you change the signature?",
	}
	b.ReportAllocs()
	for b.Loop() {
		for _, p := range prompts {
			Detect(p)
		}
	}
}

func BenchmarkDetectLong(b *testing.B) {
	// Just under the cap: the slowest prompt Detect still scans.
	prompt := strings.Repeat("Please check the log output and tell me what you find. ", MaxPromptChars/55)
	b.ReportAllocs()
	for b.Loop() {
		Detect(prompt)
	}
}

func FuzzDetect(f *testing.F) {
	for _, s := range []string{
		"No, use the existing helper in internal/util.",
		"nope - we use pytest here, not unittest",
		"Don't mock the database in these tests.",
		"We don't use testify in this codebase.",
		"That's wrong — the config lives in ~/.config.",
		"You’re wrong about the default port.",
		"From now on, run make lint before you say you're done.",
		"Remember to update docs/cli.md when flags change.",
		"Never commit directly to main.",
		"Should I use errors.Is instead of comparing strings?",
		"No, that's fine, ship it.",
		"Well, never mind, it was my cache.",
		"The error says `don't use deprecated API` — what does it mean?",
		"Fix this:\n```\n// don't do this\nnever_called()\n```",
		`The log says "stop retrying after 3 attempts", is that right?`,
		"/guardrails:gate",
		"",
		"```unterminated don't",
		strings.Repeat("panic: runtime error at main.go:12\n", 200),
	} {
		f.Add(s)
	}
	explicitCue := map[string]bool{}
	known := map[string]bool{}
	for _, c := range cues {
		known[c.signal] = true
		if c.explicit {
			explicitCue[c.signal] = true
		}
	}
	f.Fuzz(func(t *testing.T, prompt string) {
		signals, explicit, ok := Detect(prompt)
		s2, e2, ok2 := Detect(prompt)
		if !slices.Equal(signals, s2) || explicit != e2 || ok != ok2 {
			t.Fatalf("Detect(%q) not deterministic", prompt)
		}
		if ok != (len(signals) > 0) {
			t.Fatalf("ok=%v with signals %v", ok, signals)
		}
		if !ok && (explicit || signals != nil) {
			t.Fatalf("not ok but explicit=%v signals=%v", explicit, signals)
		}
		wantExplicit := false
		seen := map[string]bool{}
		for _, s := range signals {
			if !known[s] || seen[s] {
				t.Fatalf("unknown or repeated signal %q in %v", s, signals)
			}
			seen[s] = true
			wantExplicit = wantExplicit || explicitCue[s]
		}
		if explicit != wantExplicit {
			t.Fatalf("explicit=%v but signals %v", explicit, signals)
		}
		text := strings.TrimSpace(prompt)
		if ok && (strings.HasPrefix(text, "/") || len(text) > MaxPromptChars) {
			t.Fatalf("ok for ignored prompt %q", trunc(prompt))
		}
		// Surrounding whitespace is not part of the prompt.
		s3, e3, ok3 := Detect("  \n" + prompt + "\t \n")
		if !slices.Equal(signals, s3) || explicit != e3 || ok != ok3 {
			t.Fatalf("surrounding whitespace changes Detect(%q)", trunc(prompt))
		}
	})
}
