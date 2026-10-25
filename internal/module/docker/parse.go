package docker

import (
	"regexp"
	"strings"
)

// Instruction is one Dockerfile instruction after line continuations are
// joined.
type Instruction struct {
	// Cmd is the upper-cased instruction keyword, e.g. "FROM".
	Cmd string
	// Args is everything after the keyword, with continuations joined by a
	// space.
	Args string
	// Line is the 1-based line the instruction starts on.
	Line int
}

// Stage is one build stage, starting at a FROM.
type Stage struct {
	// Image is the image reference as written.
	Image string
	// Name is the stage alias from `AS name`, or "".
	Name string
	// Line is the FROM instruction's line.
	Line int
	// Instructions are the stage's instructions after FROM.
	Instructions []Instruction
}

// Dockerfile is a parsed Dockerfile.
type Dockerfile struct {
	// Args are the ARG defaults declared before the first FROM.
	Args map[string]string
	// Global are the ARG instructions before the first FROM, in order.
	Global []Instruction
	// Stages are the build stages in order.
	Stages []Stage
}

var (
	directiveRe = regexp.MustCompile(`^#\s*([a-zA-Z]+)\s*=\s*(\S+)\s*$`)
	heredocRe   = regexp.MustCompile(`<<(-?)\s*(?:"([A-Za-z0-9_]+)"|'([A-Za-z0-9_]+)'|([A-Za-z0-9_]+))`)
)

// Parse reads Dockerfile syntax: parser directives (only escape matters),
// comments, line continuations, case-insensitive keywords and, best effort,
// heredocs on RUN and COPY.
func Parse(content string) []Instruction {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	escape := byte('\\')

	i := 0
	// Parser directives must come first, before any comment, blank line or
	// instruction.
	for ; i < len(lines); i++ {
		m := directiveRe.FindStringSubmatch(strings.TrimSpace(lines[i]))
		if m == nil {
			break
		}
		if strings.EqualFold(m[1], "escape") && (m[2] == "`" || m[2] == `\`) {
			escape = m[2][0]
		}
	}

	var out []Instruction
	for i < len(lines) {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			i++
			continue
		}
		start := i
		var b strings.Builder
		for i < len(lines) {
			t := strings.TrimRight(lines[i], " \t")
			if i > start && strings.HasPrefix(strings.TrimSpace(t), "#") {
				// Comment lines inside a continuation are dropped.
				i++
				continue
			}
			i++
			if len(t) > 0 && t[len(t)-1] == escape {
				b.WriteString(strings.TrimSpace(t[:len(t)-1]))
				b.WriteByte(' ')
				continue
			}
			b.WriteString(strings.TrimSpace(t))
			break
		}
		text := strings.TrimSpace(b.String())
		cmd, args, _ := strings.Cut(text, " ")
		cmd = strings.ToUpper(strings.TrimSpace(cmd))
		args = strings.TrimSpace(args)
		if cmd == "RUN" || cmd == "COPY" || cmd == "ADD" {
			i = skipHeredocs(lines, i, args)
		}
		out = append(out, Instruction{Cmd: cmd, Args: args, Line: start + 1})
	}
	return out
}

// skipHeredocs consumes the bodies of every heredoc opened in args, starting
// at line i, and returns the next line to parse.
func skipHeredocs(lines []string, i int, args string) int {
	for _, m := range heredocRe.FindAllStringSubmatch(args, -1) {
		delim := m[2] + m[3] + m[4]
		stripTabs := m[1] == "-"
		for i < len(lines) {
			l := lines[i]
			i++
			if stripTabs {
				l = strings.TrimLeft(l, "\t")
			}
			if strings.TrimRight(l, " \t") == delim {
				break
			}
		}
	}
	return i
}

// ParseFile splits instructions into global ARGs and stages.
func ParseFile(content string) Dockerfile {
	df := Dockerfile{Args: map[string]string{}}
	for _, in := range Parse(content) {
		switch {
		case in.Cmd == "FROM":
			df.Stages = append(df.Stages, parseFrom(in))
		case len(df.Stages) == 0:
			if in.Cmd == "ARG" {
				df.Global = append(df.Global, in)
				for name, val := range parseArgs(in.Args) {
					df.Args[name] = val
				}
			}
		default:
			s := &df.Stages[len(df.Stages)-1]
			s.Instructions = append(s.Instructions, in)
		}
	}
	return df
}

// parseFrom reads a FROM instruction into a Stage with its image and optional AS name, ignoring flags like --platform.
func parseFrom(in Instruction) Stage {
	st := Stage{Line: in.Line}
	var fields []string
	for _, f := range strings.Fields(in.Args) {
		if strings.HasPrefix(f, "--") {
			continue // --platform=...
		}
		fields = append(fields, f)
	}
	if len(fields) > 0 {
		st.Image = fields[0]
	}
	if len(fields) >= 3 && strings.EqualFold(fields[1], "AS") {
		st.Name = fields[2]
	}
	return st
}

// parseArgs reads `ARG NAME[=value] ...` declarations; names without a
// default map to "".
func parseArgs(args string) map[string]string {
	out := map[string]string{}
	for _, f := range strings.Fields(args) {
		name, val, _ := strings.Cut(f, "=")
		out[name] = strings.Trim(val, `"'`)
	}
	return out
}

var varRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::?([-+])([^}]*))?\}|\$([A-Za-z_][A-Za-z0-9_]*)`)

// Expand substitutes $NAME, ${NAME}, ${NAME:-default} and ${NAME:+alt}
// from args. Unknown or empty variables without a default are left as
// written, so callers can tell the reference is unresolved.
func Expand(s string, args map[string]string) string {
	return varRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := varRe.FindStringSubmatch(m)
		name, op, word := sub[1], sub[2], sub[3]
		if name == "" {
			name = sub[4]
		}
		val, ok := args[name]
		set := ok && val != ""
		switch op {
		case "-":
			if set {
				return val
			}
			return word
		case "+":
			if set {
				return word
			}
			return ""
		}
		if set {
			return val
		}
		return m
	})
}
