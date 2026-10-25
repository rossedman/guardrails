// Package guard implements the PreToolUse policy guards.
//
// Anti-gaming guards stop Claude from weakening the standards (editing the
// guardrail config, adding suppression comments, skipping tests). Blast-
// radius guards keep irreversible infrastructure and history operations
// with the human. Every guard is a pure function of the tool input, the
// current file contents and a few facts about the environment.
package guard

import (
	"strings"

	"github.com/rossedman/guardrails/internal/hook"
)

// Decision is a guard verdict.
type Decision struct {
	// Action is hook.Allow, hook.Ask or hook.Deny.
	Action string
	// Guard names the guard that fired.
	Guard string
	// Reason is shown to Claude.
	Reason string
}

// Allowed is the zero-risk verdict.
var Allowed = Decision{Action: hook.Allow}

// severity ranks an action: deny is 2, ask is 1, anything else is 0.
func severity(action string) int {
	switch action {
	case hook.Deny:
		return 2
	case hook.Ask:
		return 1
	default:
		return 0
	}
}

// Worst returns the most severe decision; ties keep the first.
func Worst(ds ...Decision) Decision { return worst(ds...) }

// worst returns the most severe decision; ties keep the first.
func worst(ds ...Decision) Decision {
	out := Allowed
	for _, d := range ds {
		if severity(d.Action) > severity(out.Action) {
			out = d
		}
	}
	return out
}

// deny returns a Decision that denies, from the named guard and for the given reason.
func deny(guard, reason string) Decision {
	return Decision{Action: hook.Deny, Guard: guard, Reason: reason}
}

// ask returns a Decision that asks the human, from the named guard and for the given reason.
func ask(guard, reason string) Decision {
	return Decision{Action: hook.Ask, Guard: guard, Reason: reason}
}

// Message renders the decision for Claude.
func (d Decision) Message() string {
	if d.Action == hook.Allow {
		return ""
	}
	return "guardrails [" + d.Guard + "]: " + d.Reason
}

// lines splits text into lines without trailing carriage returns.
func lines(s string) []string {
	if s == "" {
		return nil
	}
	ls := strings.Split(s, "\n")
	for i, l := range ls {
		ls[i] = strings.TrimRight(l, "\r")
	}
	return ls
}

// addedLines returns lines in after that are not in before, treating both
// as multisets of whitespace-trimmed lines. Order follows after.
func addedLines(before, after string) []string {
	count := map[string]int{}
	for _, l := range lines(before) {
		count[strings.TrimSpace(l)]++
	}
	var out []string
	for _, l := range lines(after) {
		t := strings.TrimSpace(l)
		if count[t] > 0 {
			count[t]--
			continue
		}
		out = append(out, l)
	}
	return out
}
