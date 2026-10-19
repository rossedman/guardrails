package lessons

import (
	"slices"
	"strings"
	"testing"
)

func TestDetect(t *testing.T) {
	t.Parallel()
	tests := []struct {
		prompt   string
		ok       bool
		explicit bool
		signal   string // one signal that must be present when ok
	}{
		// Corrections.
		{"No, use the existing helper in internal/util.", true, false, "no"},
		{"nope - we use pytest here, not unittest", true, true, "no"},
		{"Don't mock the database in these tests.", true, false, "don't"},
		{"please don't add comments to every line", true, false, "don't"},
		{"We don't use testify in this codebase.", true, false, "we don't"},
		{"Stop adding emojis to the commit messages.", true, false, "stop"},
		{"That's wrong — the config lives in ~/.config.", true, false, "that's wrong"},
		{"that is not what I asked for", true, false, "that's wrong"},
		{"Actually, prefer table-driven tests.", true, false, "actually"},
		{"Use errors.Is instead of comparing strings.", true, false, "instead"},
		{"instead of a new file, put it in handlers.go", true, false, "instead"},
		{"I told you to run the linter first.", true, false, "i told you"},
		{"Not like that. Put the flag on the root command.", true, false, "not like that"},
		{"Why did you delete the migration?", true, false, "why did you"},
		{"Revert that change to go.mod.", true, false, "revert"},
		{"undo the rename, it broke the API", true, false, "revert"},
		{"Avoid global state in this package.", true, false, "avoid"},
		{"you shouldn't edit generated files", true, false, "you shouldn't"},
		{"You don't need to run go mod tidy every time.", true, false, "you don't need"},
		{"You’re wrong about the default port.", true, false, "that's wrong"},
		{"OK. Do not touch the vendor directory.", true, false, "don't"},

		// Standing instructions.
		{"From now on, run make lint before you say you're done.", true, true, "from now on"},
		{"going forward keep PRs under 400 lines", true, true, "from now on"},
		{"In the future, always add a changelog entry.", true, true, "from now on"},
		{"Remember to update docs/cli.md when flags change.", true, true, "remember"},
		{"In this repo we use conventional commits.", true, true, "in this repo"},
		{"Always run the race detector.", true, true, "always"},
		{"Never commit directly to main.", true, true, "never"},
		{"Our convention is snake_case for JSON keys.", true, true, "convention"},
		{"We prefer small functions over long ones.", true, true, "we prefer"},
		{"We use ruff for formatting. Set it up.", true, true, "we use"},

		// Not corrections.
		{"", false, false, ""},
		{"Why does this test fail?", false, false, ""},
		{"Add a --json flag to the insights command.", false, false, ""},
		{"No tests fail now, can you open a PR?", false, false, ""},
		{"No, that's fine, ship it.", false, false, ""},
		{"no worries, carry on", false, false, ""},
		{"Note that the API returns 404 for missing users.", false, false, ""},
		{"I don't know why the build is slow, can you look?", false, false, ""},
		{"Nothing works after the upgrade; please investigate.", false, false, ""},
		{"/guardrails:gate", false, false, ""},
		{"/plugin install guardrails@guardrails", false, false, ""},
		{"Should I use errors.Is instead of comparing strings?", false, false, ""},
		{"We use Postgres 16, how do I connect from the test?", false, false, ""},
		{"Actually, that works now. Thanks!", false, false, ""},
		{"Well, never mind, it was my cache.", false, false, ""},
		{"It always fails on CI but never locally.", false, false, ""},
		{"The error says `don't use deprecated API` — what does it mean?", false, false, ""},
		{"Fix this:\n```\n// don't do this\nnever_called()\n```", false, false, ""},
		{`The log says "stop retrying after 3 attempts", is that right?`, false, false, ""},
		{"Run the tests and tell me what you find.", false, false, ""},
		{strings.Repeat("panic: runtime error at main.go:12\n", 200), false, false, ""},
		{"Here's the trace:\n```\n" + strings.Repeat("goroutine 1 [running]:\n", 20) + "```\nhm", false, false, ""},
	}
	for _, tc := range tests {
		signals, explicit, ok := Detect(tc.prompt)
		if ok != tc.ok || explicit != tc.explicit {
			t.Errorf("Detect(%q) = %v explicit=%v ok=%v; want ok=%v explicit=%v", trunc(tc.prompt), signals, explicit, ok, tc.ok, tc.explicit)
			continue
		}
		if tc.ok && !slices.Contains(signals, tc.signal) {
			t.Errorf("Detect(%q) signals = %v; want %q", trunc(tc.prompt), signals, tc.signal)
		}
		if !tc.ok && signals != nil {
			t.Errorf("Detect(%q) returned signals %v without ok", trunc(tc.prompt), signals)
		}
	}
}

func TestDetectTooLong(t *testing.T) {
	t.Parallel()
	if _, _, ok := Detect("Don't do that. " + strings.Repeat("x", MaxPromptChars)); ok {
		t.Error("prompts over MaxPromptChars are ignored")
	}
}

func trunc(s string) string {
	if len(s) > 60 {
		return s[:60] + "…"
	}
	return s
}
