package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rossedman/guardrails/internal/lessons"
)

func newLessonsCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lessons",
		Short: "List the corrections you gave Claude, to turn into rules",
		Long: `List the corrections guardrails recorded when you told Claude it got
something wrong ("don't mock the database") or set a standing rule ("from
now on, run make lint"). They stay on this machine.

Lessons marked * were explicit: you asked for them to be remembered. The
/guardrails:learn skill drafts .claude/rules additions from them for you to
review; clear the ones you've handled with --clear.`,
		Example: `  guardrails lessons
  guardrails lessons --all-repos --json
  guardrails lessons --clear --id 3f9a1c0b2e   # handled one
  guardrails lessons --clear                   # all of this repo's`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			v := a.V
			ids := v.GetStringSlice("lessons.id")
			if len(ids) > 0 && !v.GetBool("lessons.clear") {
				return usageError{errors.New("--id only applies with --clear")}
			}
			repo := ""
			if !v.GetBool("lessons.all-repos") {
				r, err := a.repo(cmd.Context())
				if err != nil {
					return err
				}
				repo = r
			}
			if v.GetBool("lessons.clear") {
				n, err := lessons.Clear(a.DataDir(), repo, ids)
				if err != nil {
					return err
				}
				fmt.Fprintf(a.Stdout, "guardrails lessons: cleared %d lesson(s)\n", n)
				return nil
			}
			all, _, err := lessons.Load(a.DataDir())
			if err != nil {
				return err
			}
			list := []lessons.Lesson{}
			for _, l := range all {
				if repo == "" || l.Repo == repo {
					list = append(list, l)
				}
			}
			sort.SliceStable(list, func(i, j int) bool { return list[i].Time.After(list[j].Time) })
			if v.GetBool("lessons.json") {
				enc := json.NewEncoder(a.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(list)
			}
			printLessons(a.Stdout, list, repo == "")
			return nil
		},
	}
	f := cmd.Flags()
	f.Bool("all-repos", false, "include every repository, not just this one")
	f.Bool("json", false, "print the lessons as JSON")
	f.Bool("clear", false, "remove this repo's lessons (or every repo's with --all-repos)")
	f.StringSlice("id", nil, "with --clear, remove only these lessons")
	a.bind(cmd, "lessons")
	return cmd
}

func printLessons(w io.Writer, list []lessons.Lesson, allRepos bool) {
	scope := "this repo"
	if allRepos {
		scope = "all repos"
	}
	if len(list) == 0 {
		fmt.Fprintf(w, "guardrails lessons: no corrections recorded for %s yet.\n", scope)
		return
	}
	fmt.Fprintf(w, "guardrails lessons — %d recorded, %s\n\n", len(list), scope)
	for _, l := range list {
		mark := " "
		if l.Explicit {
			mark = "*"
		}
		line := strings.TrimSpace(strings.SplitN(l.Prompt, "\n", 2)[0])
		if r := []rune(line); len(r) > 72 {
			line = string(r[:71]) + "…"
		}
		fmt.Fprintf(w, "  %s  %s %s %s\n", l.Time.Local().Format("2006-01-02"), l.ID, mark, line)
		if allRepos {
			fmt.Fprintf(w, "  %s  %s\n", strings.Repeat(" ", 10+len(l.ID)), l.Repo)
		}
	}
	fmt.Fprintf(w, "\n* you asked for it to be remembered\nRun /guardrails:learn in Claude Code to turn these into rules.\n")
}
