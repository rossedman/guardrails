package cli

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"text/template"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// Help and usage share the brand palette: amber headings and command names,
// examples with dimmed comments. Colors only appear in a terminal; NO_COLOR
// turns them off.

const helpTemplate = `{{with (or .Long .Short)}}{{. | trimTrailingWhitespaces}}

{{end}}{{if or .Runnable .HasSubCommands}}{{template "usage" .}}{{end}}`

const usageTemplate = `{{heading "Usage:"}}{{if and .Runnable (not .HasAvailableSubCommands)}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]{{end}}{{if gt (len .Aliases) 0}}

{{heading "Aliases:"}}
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

{{heading "Examples:"}}
{{examples .Example}}{{end}}{{if .HasAvailableSubCommands}}{{$cmds := .Commands}}{{if eq (len .Groups) 0}}

{{heading "Commands:"}}{{range $cmds}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{command (rpad .Name .NamePadding)}} {{.Short}}{{end}}{{end}}{{else}}{{range $group := .Groups}}

{{heading .Title}}{{range $cmds}}{{if (and (eq .GroupID $group.ID) (or .IsAvailableCommand (eq .Name "help")))}}
  {{command (rpad .Name .NamePadding)}} {{.Short}}{{end}}{{end}}{{end}}{{if not .AllChildCommandsHaveGroup}}

{{heading "More:"}}{{range $cmds}}{{if (and (eq .GroupID "") (or .IsAvailableCommand (eq .Name "help")))}}
  {{command (rpad .Name .NamePadding)}} {{.Short}}{{end}}{{end}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

{{heading "Flags:"}}
{{flags (.LocalFlags.FlagUsages | trimTrailingWhitespaces)}}{{end}}{{if .HasAvailableInheritedFlags}}

{{heading "Global flags:"}}
{{flags (.InheritedFlags.FlagUsages | trimTrailingWhitespaces)}}{{end}}{{if .HasAvailableSubCommands}}

{{dim (printf "Run \"%s [command] --help\" for more about a command." .CommandPath)}}{{end}}
`

// ANSI styles in the docs' palette.
const (
	styleHeading = "\x1b[1;38;2;245;184;61m" // bold amber
	styleName    = "\x1b[38;2;245;184;61m"   // amber
	styleCode    = "\x1b[38;2;250;214;140m"  // light amber
	styleDim     = "\x1b[38;2;122;133;158m"  // slate
	styleReset   = "\x1b[0m"
)

// isTerminal reports whether w is a terminal; tests replace it.
var isTerminal = func(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// flagNameRE finds flag names at the start of each flag usage line, like
// "  -q, --quiet" or "      --base string".
var flagNameRE = regexp.MustCompile(`(?m)^\s+(-\w, )?--[\w-]+`)

// setupHelp gives every command the styled help and usage.
func (a *App) setupHelp(root *cobra.Command) {
	cobra.EnableCommandSorting = false
	root.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		_ = a.renderHelp(cmd.OutOrStdout(), cmd)
	})
	root.SetUsageFunc(func(cmd *cobra.Command) error {
		return a.renderUsage(cmd.OutOrStderr(), cmd)
	})
}

func (a *App) renderHelp(w io.Writer, cmd *cobra.Command) error {
	return a.render(w, cmd, helpTemplate)
}

func (a *App) renderUsage(w io.Writer, cmd *cobra.Command) error {
	return a.render(w, cmd, `{{template "usage" .}}`)
}

func (a *App) render(w io.Writer, cmd *cobra.Command, text string) error {
	t := template.New("help").Funcs(a.helpFuncs(isTerminal(w) && a.Getenv("NO_COLOR") == ""))
	template.Must(t.New("usage").Parse(usageTemplate))
	template.Must(t.Parse(text))
	return t.Execute(w, cmd)
}

func (a *App) helpFuncs(color bool) template.FuncMap {
	style := func(s, text string) string {
		if !color || text == "" {
			return text
		}
		return s + text + styleReset
	}
	return template.FuncMap{
		"trimTrailingWhitespaces": func(s string) string { return strings.TrimRight(s, " \t\n") },
		"rpad":                    func(s string, n int) string { return fmt.Sprintf("%-*s", n, s) },
		"heading":                 func(s string) string { return style(styleHeading, s) },
		"dim":                     func(s string) string { return style(styleDim, s) },
		"command": func(s string) string {
			// Pad first, so color codes don't upset the alignment.
			trimmed := strings.TrimRight(s, " ")
			return style(styleName, trimmed) + s[len(trimmed):]
		},
		"examples": func(s string) string {
			lines := strings.Split(s, "\n")
			for i, l := range lines {
				t := strings.TrimSpace(l)
				indent := l[:len(l)-len(strings.TrimLeft(l, " "))]
				switch {
				case strings.HasPrefix(t, "#"):
					lines[i] = indent + style(styleDim, t)
				case t != "":
					lines[i] = indent + style(styleCode, t)
				}
			}
			return strings.Join(lines, "\n")
		},
		"flags": func(s string) string {
			return flagNameRE.ReplaceAllStringFunc(s, func(m string) string { return style(styleName, m) })
		},
	}
}
