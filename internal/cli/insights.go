package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/rossedman/guardrails/internal/insights"
)

// insights handles `guardrails insights`: a summary of gate history from
// runs.jsonl, and rules that recur often enough to teach Claude up front.
func (a *App) insights(ctx context.Context, args []string) int {
	fl := newFlags("insights", a.Stderr)
	days := fl.Int("days", 30, "window in days")
	allRepos := fl.Bool("all-repos", false, "include every repository, not just this one")
	minSessions := fl.Int("min-sessions", 5, "sessions a rule must trip in to become a rule candidate")
	asJSON := fl.Bool("json", false, "print the report as JSON")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	repo := ""
	if !*allRepos {
		r, err := a.repo(ctx)
		if err != nil {
			fmt.Fprintf(a.Stderr, "guardrails: %v\n", err)
			return 1
		}
		repo = r
	}
	recs, malformed, err := insights.Load(a.DataDir())
	if err != nil {
		fmt.Fprintf(a.Stderr, "guardrails: %v\n", err)
		return 1
	}
	rep := insights.Summarize(recs, insights.Options{Days: *days, Repo: repo, MinSessions: *minSessions, Now: a.Now()})
	rep.Malformed = malformed

	if *asJSON {
		enc := json.NewEncoder(a.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(rep)
		return 0
	}
	printInsights(a.Stdout, rep, *days)
	return 0
}

func printInsights(w io.Writer, rep insights.Report, days int) {
	scope := "this repo"
	if rep.Repo == "" {
		scope = "all repos"
	}
	if rep.GateRuns == 0 {
		fmt.Fprintf(w, "guardrails insights: no gate runs in the last %d days (%s) yet.\n", days, scope)
		return
	}
	fmt.Fprintf(w, "guardrails insights — last %d days, %s\n\n", days, scope)
	fmt.Fprintf(w, "  first-pass rate   %.0f%% (%d of %d turns passed the gate first time)\n", rep.FirstPassRate*100, rep.FirstPass, rep.Turns)
	fmt.Fprintf(w, "  blocks per turn   %.1f\n", rep.BlocksPerTurn)
	fmt.Fprintf(w, "  hand-offs         %d\n", rep.HandOffs)
	fmt.Fprintf(w, "  sessions          %d (%d gate runs)\n", rep.Sessions, rep.GateRuns)
	fmt.Fprintf(w, "  gate time         p50 %s, p95 %s\n", ms(rep.GateMillisP50), ms(rep.GateMillisP95))
	if rep.Timeouts > 0 {
		fmt.Fprintf(w, "  timeouts          %d gates exceeded their budget\n", rep.Timeouts)
	}
	if rep.Malformed > 0 {
		fmt.Fprintf(w, "  skipped           %d malformed log lines\n", rep.Malformed)
	}

	if len(rep.TopRules) > 0 {
		fmt.Fprintf(w, "\nTop rules tripped\n")
		fmt.Fprintf(w, "  %-36s %8s %6s  %s\n", "RULE", "SESSIONS", "TIMES", "EXAMPLE")
		for _, r := range rep.TopRules {
			ex := ""
			if len(r.Examples) > 0 {
				ex = r.Examples[0]
			}
			fmt.Fprintf(w, "  %-36s %8d %6d  %s\n", r.Name(), r.Sessions, r.Count, ex)
		}
	}

	if len(rep.RuleCandidates) > 0 {
		fmt.Fprintf(w, "\nRule candidates (tripped in many sessions; teach them up front)\n")
		for _, r := range rep.RuleCandidates {
			target := ".claude/rules/" + r.RulesFile
			if r.RulesFile == "" {
				target = "a rules file (language unknown)"
			}
			fmt.Fprintf(w, "  %-36s %d sessions → %s\n", r.Name(), r.Sessions, target)
		}
		fmt.Fprintf(w, "\nRun /guardrails:insights in Claude Code to draft these rules for review.\n")
	}
}

func ms(v int64) string {
	if v >= 1000 {
		return fmt.Sprintf("%.1fs", float64(v)/1000)
	}
	return fmt.Sprintf("%dms", v)
}
