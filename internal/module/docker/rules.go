package docker

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/rossedman/guardrails/internal/config"
	"github.com/rossedman/guardrails/internal/feedback"
)

// Rule is one native Dockerfile rule.
type Rule struct {
	// ID is the stable rule id shown to Claude, e.g. GR-DOCKER-001.
	ID string
	// Check is the bracketed check name in reports.
	Check string
	// Fix is the one-line hint for the group.
	Fix string
	// Enabled reports whether config turns the rule on.
	Enabled func(config.Docker) bool
	// Find returns the violations in a parsed Dockerfile.
	Find func(Dockerfile) []Finding
}

// Finding is one violation, before it is tied to a file.
type Finding struct {
	Line    int
	Message string
}

func always(config.Docker) bool { return true }

// Rules are the native checks, in report order.
var Rules = []Rule{
	{
		ID: "GR-DOCKER-001", Check: "digest-pins",
		Fix:     "pin with `image:tag@sha256:<digest>` (docker buildx imagetools inspect <image:tag>)",
		Enabled: func(d config.Docker) bool { return d.RequireDigestPins },
		Find:    digestFindings,
	},
	{
		ID: "GR-DOCKER-002", Check: "non-root",
		Fix:     "add a non-root USER in the final stage",
		Enabled: func(d config.Docker) bool { return d.RequireNonRoot },
		Find:    rootFindings,
	},
	{
		ID: "GR-DOCKER-003", Check: "image-tag",
		Fix: "use an explicit version tag, e.g. `alpine:3.20`",
		// With digest pins required, GR-DOCKER-001 already covers these.
		Enabled: func(d config.Docker) bool { return !d.RequireDigestPins },
		Find:    tagFindings,
	},
	{
		ID: "GR-DOCKER-004", Check: "add-vs-copy",
		Fix:     "use COPY for local files; ADD is for URLs and archives to extract",
		Enabled: always, Find: addFindings,
	},
	{
		ID: "GR-DOCKER-005", Check: "apt-recommends",
		Fix:     "add `--no-install-recommends` to `apt-get install`",
		Enabled: always, Find: aptRecommendsFindings,
	},
	{
		ID: "GR-DOCKER-006", Check: "apt-cleanup",
		Fix:     "end the same RUN with `&& rm -rf /var/lib/apt/lists/*` (or use a cache mount)",
		Enabled: always, Find: aptCleanupFindings,
	},
	{
		ID: "GR-DOCKER-007", Check: "apk-cache",
		Fix:     "use `apk add --no-cache`",
		Enabled: always, Find: apkFindings,
	},
	{
		ID: "GR-DOCKER-008", Check: "build-secrets",
		Fix:     "pass secrets with `RUN --mount=type=secret`, never ARG or ENV (they persist in image history)",
		Enabled: always, Find: secretFindings,
	},
}

// Lint runs every enabled rule over content and returns one failing check
// per rule that found something, with issues attributed to file.
func Lint(cfg config.Docker, file, content string) []feedback.Check {
	return lintParsed(cfg, file, ParseFile(content))
}

func lintParsed(cfg config.Docker, file string, df Dockerfile) []feedback.Check {
	var checks []feedback.Check
	for _, r := range Rules {
		if !r.Enabled(cfg) {
			continue
		}
		var issues []feedback.Issue
		for _, f := range r.Find(df) {
			issues = append(issues, feedback.Issue{File: file, Line: f.Line, Rule: r.ID, Message: f.Message})
		}
		if len(issues) == 0 {
			continue
		}
		checks = append(checks, feedback.Check{
			Name: r.Check, Failed: true,
			Summary: fmt.Sprintf("%d issue%s", len(issues), plural(len(issues))),
			Issues:  issues, Fix: r.Fix,
		})
	}
	return checks
}

// externalImages yields stages whose image is not scratch or an earlier
// stage, with the image expanded from global ARG defaults.
func externalImages(df Dockerfile, fn func(st Stage, expanded string)) {
	stages := map[string]bool{}
	for _, st := range df.Stages {
		expanded := Expand(st.Image, df.Args)
		lower := strings.ToLower(expanded)
		if st.Image != "" && lower != "scratch" && !stages[lower] {
			fn(st, expanded)
		}
		if st.Name != "" {
			stages[strings.ToLower(st.Name)] = true
		}
	}
}

func digestFindings(df Dockerfile) []Finding {
	var out []Finding
	externalImages(df, func(st Stage, expanded string) {
		switch {
		case strings.Contains(expanded, "@sha256:"):
		case strings.Contains(expanded, "$"):
			out = append(out, Finding{st.Line, fmt.Sprintf("FROM %s uses a build arg without a default; give it a digest-pinned default", st.Image)})
		default:
			out = append(out, Finding{st.Line, fmt.Sprintf("FROM %s is not pinned by digest", expanded)})
		}
	})
	return out
}

func tagFindings(df Dockerfile) []Finding {
	var out []Finding
	externalImages(df, func(st Stage, expanded string) {
		if strings.Contains(expanded, "@") || strings.Contains(expanded, "$") {
			return
		}
		// The tag follows the last colon after the last slash (a colon
		// before it is a registry port).
		name := expanded[strings.LastIndex(expanded, "/")+1:]
		_, tag, hasTag := strings.Cut(name, ":")
		switch {
		case !hasTag:
			out = append(out, Finding{st.Line, fmt.Sprintf("FROM %s has no tag, so it floats to latest", expanded)})
		case tag == "latest":
			out = append(out, Finding{st.Line, fmt.Sprintf("FROM %s uses the latest tag", expanded)})
		}
	})
	return out
}

// rootFindings reports a final stage that runs as root: its last USER (or
// that of the stage it builds on) is root, 0 or missing. Base images named
// nonroot or rootless are trusted to set their own user.
func rootFindings(df Dockerfile) []Finding {
	if len(df.Stages) == 0 {
		return nil
	}
	final := df.Stages[len(df.Stages)-1]
	user, line, base := effectiveUser(df, len(df.Stages)-1)
	switch {
	case user == "":
		lower := strings.ToLower(Expand(base, df.Args))
		if strings.Contains(lower, "nonroot") || strings.Contains(lower, "rootless") {
			return nil
		}
		return []Finding{{final.Line, "the final stage sets no USER, so the container runs as root"}}
	case isRoot(user):
		return []Finding{{line, fmt.Sprintf("the final stage runs as USER %s", user)}}
	}
	return nil
}

// effectiveUser returns the last USER in stage i, following FROM <stage>
// chains, with its line and the external base image of the chain.
func effectiveUser(df Dockerfile, i int) (user string, line int, base string) {
	seen := map[int]bool{}
	for i >= 0 && !seen[i] {
		seen[i] = true
		st := df.Stages[i]
		for j := len(st.Instructions) - 1; j >= 0; j-- {
			if in := st.Instructions[j]; in.Cmd == "USER" {
				return strings.TrimSpace(in.Args), in.Line, st.Image
			}
		}
		base = st.Image
		next := -1
		for k := 0; k < i; k++ {
			if df.Stages[k].Name != "" && strings.EqualFold(df.Stages[k].Name, st.Image) {
				next = k
			}
		}
		i = next
	}
	return "", 0, base
}

func isRoot(user string) bool {
	name, _, _ := strings.Cut(user, ":")
	name = strings.TrimSpace(name)
	return name == "root" || name == "0"
}

// instructions yields every instruction in every stage.
func instructions(df Dockerfile, fn func(Instruction)) {
	for _, st := range df.Stages {
		for _, in := range st.Instructions {
			fn(in)
		}
	}
}

var archiveRe = regexp.MustCompile(`(?i)\.(tar|tar\.gz|tgz|tar\.bz2|tbz2?|tar\.xz|txz|tar\.zst)$`)

func addFindings(df Dockerfile) []Finding {
	var out []Finding
	instructions(df, func(in Instruction) {
		if in.Cmd != "ADD" || strings.Contains(in.Args, "<<") {
			return
		}
		var args []string
		for _, f := range strings.Fields(in.Args) {
			if !strings.HasPrefix(f, "--") {
				args = append(args, f)
			}
		}
		if len(args) < 2 || strings.HasPrefix(args[0], "[") {
			return
		}
		for _, src := range args[:len(args)-1] {
			lower := strings.ToLower(src)
			if strings.Contains(lower, "://") || strings.HasPrefix(lower, "git@") || archiveRe.MatchString(src) {
				return
			}
		}
		out = append(out, Finding{in.Line, fmt.Sprintf("ADD %s copies local files; COPY is explicit and does not extract or fetch", strings.Join(args, " "))})
	})
	return out
}

var (
	aptInstallRe = regexp.MustCompile(`\bapt(-get)?\s+(\S+\s+)*install\b`)
	apkAddRe     = regexp.MustCompile(`\bapk\s+(\S+\s+)*add\b`)
)

func aptRecommendsFindings(df Dockerfile) []Finding {
	var out []Finding
	instructions(df, func(in Instruction) {
		if in.Cmd == "RUN" && aptInstallRe.MatchString(in.Args) && !strings.Contains(in.Args, "--no-install-recommends") {
			out = append(out, Finding{in.Line, "apt-get install without --no-install-recommends pulls in optional packages"})
		}
	})
	return out
}

func aptCleanupFindings(df Dockerfile) []Finding {
	var out []Finding
	instructions(df, func(in Instruction) {
		if in.Cmd != "RUN" || !aptInstallRe.MatchString(in.Args) {
			return
		}
		if strings.Contains(in.Args, "/var/lib/apt/lists") || strings.Contains(in.Args, "type=cache") {
			return
		}
		out = append(out, Finding{in.Line, "apt-get install leaves /var/lib/apt/lists in the layer"})
	})
	return out
}

func apkFindings(df Dockerfile) []Finding {
	var out []Finding
	instructions(df, func(in Instruction) {
		if in.Cmd != "RUN" || !apkAddRe.MatchString(in.Args) {
			return
		}
		if strings.Contains(in.Args, "--no-cache") || strings.Contains(in.Args, "type=cache") {
			return
		}
		out = append(out, Finding{in.Line, "apk add without --no-cache leaves the package index in the layer"})
	})
	return out
}

var secretNameRe = regexp.MustCompile(`(?i)(TOKEN|PASSWORD|PASSWD|SECRET|API_?KEY|PRIVATE_?KEY|ACCESS_?KEY|CREDENTIALS?)`)

// secretName reports whether an ARG or ENV name looks like it holds a
// secret. Names pointing at a secret (…_FILE, …_PATH) are fine.
func secretName(name string) bool {
	upper := strings.ToUpper(name)
	if strings.HasSuffix(upper, "_FILE") || strings.HasSuffix(upper, "_PATH") || strings.HasSuffix(upper, "_DIR") {
		return false
	}
	return secretNameRe.MatchString(name)
}

func secretFindings(df Dockerfile) []Finding {
	var out []Finding
	check := func(in Instruction) {
		for _, name := range declaredNames(in) {
			if secretName(name) {
				out = append(out, Finding{in.Line, fmt.Sprintf("%s %s looks like a secret; it is stored in the image history", in.Cmd, name)})
			}
		}
	}
	for _, in := range df.Global {
		check(in)
	}
	instructions(df, func(in Instruction) {
		if in.Cmd == "ARG" || in.Cmd == "ENV" {
			check(in)
		}
	})
	return out
}

// declaredNames returns the variable names an ARG or ENV declares,
// handling `KEY=value` pairs and the legacy `ENV KEY value` form.
func declaredNames(in Instruction) []string {
	fields := strings.Fields(in.Args)
	if len(fields) == 0 {
		return nil
	}
	if in.Cmd == "ENV" && !strings.Contains(fields[0], "=") {
		return []string{fields[0]}
	}
	var out []string
	for _, f := range fields {
		name, _, hasValue := strings.Cut(f, "=")
		if in.Cmd == "ENV" && !hasValue {
			continue // part of a quoted value
		}
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}
