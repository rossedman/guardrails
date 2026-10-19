package cli

import (
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
"guardrails learn" shows this with your recorded corrections, and the
/guardrails:learn skill drafts the rules for you to review.`,
		Example: `  guardrails insights
  guardrails insights --days 7 --all-repos
  guardrails insights --json | jq .rule_candidates`,
		Hidden: true,
		Args:   usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			v := a.V
			days := v.GetInt("insights.days")
			repo, err := a.scope(cmd.Context(), v.GetBool("insights.all-repos"))
			if err != nil {
				return err
			}
			rep, err := a.insightsReport(repo, days, v.GetInt("insights.min-sessions"))
			if err != nil {
				return err
			}
			if v.GetBool("insights.json") {
				return writeJSON(a.Stdout, rep)
			}
			printInsights(a.Stdout, rep, days, true)
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

// insightsReport summarizes the gate history for repo ("" = all repos).
func (a *App) insightsReport(repo string, days, minSessions int) (insights.Report, error) {
	recs, malformed, err := insights.Load(a.DataDir())
	if err != nil {
		return insights.Report{}, err
	}
	rep := insights.Summarize(recs, insights.Options{Days: days, Repo: repo, MinSessions: minSessions, Now: a.Now()})
	rep.Malformed = malformed
	return rep, nil
}

// printInsights prints the report; hint adds the pointer to the skill.
func printInsights(w io.Writer, rep insights.Report, days int, hint bool) {
	scope := "this repo"
	if rep.Repo == "" {
		scope = "all repos"
	}
	if rep.GateRuns == 0 {
		fmt.Fprintf(w, "Gate history: no gate runs in the last %d days (%s) yet.\n", days, scope)
		return
	}
	fmt.Fprintf(w, "Gate history — last %d days, %s\n\n", days, scope)
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
		if hint {
			fmt.Fprintf(w, "\nRun /guardrails:learn in Claude Code to draft these rules for review.\n")
		}
	}
}

func ms(v int64) string {
	if v >= 1000 {
		return fmt.Sprintf("%.1fs", float64(v)/1000)
	}
	return fmt.Sprintf("%dms", v)
}
