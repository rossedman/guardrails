package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/rossedman/guardrails/internal/insights"
	"github.com/rossedman/guardrails/internal/lessons"
)

// newLearnCmd builds the learn command.
func newLearnCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "learn",
		Short: "What Claude keeps getting wrong, from the gate and from your corrections",
		Long: `Show what's worth teaching Claude up front, from two sources:

  the gate     how often Claude passes first time, the rules it trips most,
               and rule candidates: rules tripped in --min-sessions sessions
  you          the corrections you gave Claude ("don't mock the database")
               and standing rules ("from now on, run make lint"), recorded
               when you sent them

Everything stays on this machine. In Claude Code, /guardrails:learn turns
this into .claude/rules additions for you to review; clear the corrections
you've handled with --clear.`,
		Example: `  guardrails learn
  guardrails learn --days 7 --all-repos
  guardrails learn --json
  guardrails learn --clear --id 3f9a1c0b2e   # a handled correction
  guardrails learn --clear                   # all of this repo's`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			v := a.V
			allRepos := v.GetBool("learn.all-repos")
			if done, err := a.clearLessons(cmd.Context(), "learn", allRepos); done || err != nil {
				return err
			}
			repo, err := a.scope(cmd.Context(), allRepos)
			if err != nil {
				return err
			}
			days := v.GetInt("learn.days")
			rep, err := a.insightsReport(repo, days, v.GetInt("learn.min-sessions"))
			if err != nil {
				return err
			}
			list, err := a.lessonsFor(repo)
			if err != nil {
				return err
			}
			if v.GetBool("learn.json") {
				return writeJSON(a.Stdout, struct {
					Insights insights.Report  `json:"insights"`
					Lessons  []lessons.Lesson `json:"lessons"`
				}{rep, list})
			}
			printInsights(a.Stdout, rep, days, false)
			fmt.Fprintln(a.Stdout)
			printLessons(a.Stdout, list, allRepos, false)
			if len(rep.RuleCandidates) > 0 || len(list) > 0 {
				fmt.Fprintf(a.Stdout, "\nRun /guardrails:learn in Claude Code to draft rules from these for your review.\n")
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.Int("days", 30, "gate history window in days")
	f.Bool("all-repos", false, "include every repository, not just this one")
	f.Int("min-sessions", 5, "sessions a rule must trip in to become a rule candidate")
	f.Bool("json", false, `print {"insights": ..., "lessons": [...]} as JSON`)
	f.Bool("clear", false, "remove this repo's recorded corrections (every repo's with --all-repos)")
	f.StringSlice("id", nil, "with --clear, remove only these corrections")
	a.bind(cmd, "learn")
	return cmd
}
