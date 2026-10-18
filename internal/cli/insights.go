package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/rossedman/guardrails/internal/insights"
)

func newInsightsCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "insights",
		Short: "Summarize gate history and suggest rules to teach Claude",
		Long: `Summarize the gate history guardrails logs: how often Claude passes the
gate first time, blocks per turn, hand-offs, gate time, and the rules that
trip most.

Rules that trip in at least --min-sessions sessions become rule candidates:
standards worth writing into .claude/rules/ so Claude meets them up front.
The /guardrails:insights skill drafts those rules for you to review.`,
		Example: `  guardrails insights
  guardrails insights --days 7 --all-repos
  guardrails insights --json | jq .rule_candidates`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			v := a.V
			days, minSessions := v.GetInt("insights.days"), v.GetInt("insights.min-sessions")
			repo := ""
			if !v.GetBool("insights.all-repos") {
				r, err := a.repo(cmd.Context())
				if err != nil {
					return err
				}
				repo = r
			}
			recs, malformed, err := insights.Load(a.DataDir())
			if err != nil {
				return err
			}
			rep := insights.Summarize(recs, insights.Options{Days: days, Repo: repo, MinSessions: minSessions, Now: a.Now()})
			rep.Malformed = malformed

			if v.GetBool("insights.json") {
				enc := json.NewEncoder(a.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(rep)
			}
			printInsights(a.Stdout, rep, days)
			return nil
		},
	}
	f := cmd.Flags()
	f.Int("days", 30, "window in days")
	f.Bool("all-repos", false, "include every repository, not just this one")
	f.Int("min-sessions", 5, "sessions a rule must trip in to become a rule candidate")
	f.Bool("json", false, "print the report as JSON")
	a.bind(cmd, "insights")
	return cmd
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
