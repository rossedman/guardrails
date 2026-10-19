package cli

import (
	"context"
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
review; clear the ones you've handled with --clear. "guardrails learn"
shows these with the gate history.`,
		Example: `  guardrails lessons
  guardrails lessons --all-repos --json
  guardrails lessons --clear --id 3f9a1c0b2e   # handled one
  guardrails lessons --clear                   # all of this repo's`,
		Hidden: true,
		Args:   usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			v := a.V
			allRepos := v.GetBool("lessons.all-repos")
			if done, err := a.clearLessons(cmd.Context(), "lessons", allRepos); done || err != nil {
				return err
			}
			repo, err := a.scope(cmd.Context(), allRepos)
			if err != nil {
				return err
			}
			list, err := a.lessonsFor(repo)
			if err != nil {
				return err
			}
			if v.GetBool("lessons.json") {
				return writeJSON(a.Stdout, list)
			}
			printLessons(a.Stdout, list, allRepos, true)
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

// scope is the repo a report covers: this one, or "" for all repos.
func (a *App) scope(ctx context.Context, allRepos bool) (string, error) {
	if allRepos {
		return "", nil
	}
	return a.repo(ctx)
}

// lessonsFor lists the lessons recorded for repo ("" = all), newest first.
func (a *App) lessonsFor(repo string) ([]lessons.Lesson, error) {
	all, _, err := lessons.Load(a.DataDir())
	if err != nil {
		return nil, err
	}
	list := []lessons.Lesson{}
	for _, l := range all {
		if repo == "" || l.Repo == repo {
			list = append(list, l)
		}
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].Time.After(list[j].Time) })
	return list, nil
}

// clearLessons handles --clear and --id for the command whose flags live
// under prefix. It reports whether it cleared, which ends the command.
func (a *App) clearLessons(ctx context.Context, prefix string, allRepos bool) (bool, error) {
	ids := a.V.GetStringSlice(prefix + ".id")
	if !a.V.GetBool(prefix + ".clear") {
		if len(ids) > 0 {
			return true, usageError{errors.New("--id only applies with --clear")}
		}
		return false, nil
	}
	repo, err := a.scope(ctx, allRepos)
	if err != nil {
		return true, err
	}
	n, err := lessons.Clear(a.DataDir(), repo, ids)
	if err != nil {
		return true, err
	}
	fmt.Fprintf(a.Stdout, "guardrails: cleared %d lesson(s)\n", n)
	return true, nil
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// printLessons lists lessons; hint adds the pointer to the skill.
func printLessons(w io.Writer, list []lessons.Lesson, allRepos, hint bool) {
	scope := "this repo"
	if allRepos {
		scope = "all repos"
	}
	if len(list) == 0 {
		fmt.Fprintf(w, "Your corrections: none recorded for %s yet.\n", scope)
		return
	}
	fmt.Fprintf(w, "Your corrections — %d recorded, %s\n\n", len(list), scope)
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
	fmt.Fprintf(w, "\n* you asked for it to be remembered\n")
	if hint {
		fmt.Fprintf(w, "Run /guardrails:learn in Claude Code to turn these into rules.\n")
	}
}
