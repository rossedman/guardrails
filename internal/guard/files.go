package guard

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/rossedman/guardrails/internal/config"
	"github.com/rossedman/guardrails/internal/hook"
)

// FileChange is a proposed edit, normalized across Edit, MultiEdit and
// Write.
type FileChange struct {
	// Path is the absolute file path.
	Path string
	// Rel is Path relative to the repo root, with forward slashes.
	Rel string
	// Before is the current content; empty for a new file.
	Before string
	// After is the content once the edit applies.
	After string
	// Exists reports whether the file exists today.
	Exists bool
}

// NewFileChange builds a FileChange from a tool call. before is the current
// file content (exists=false for a new file). For Edit and MultiEdit, After
// is computed by applying each replacement to Before; a replacement whose
// old string is not found contributes its new string as an appended
// fragment so guards still see what Claude meant to write.
func NewFileChange(repoRoot, tool string, t hook.ToolInput, before string, exists bool) FileChange {
	fc := FileChange{Path: t.FilePath, Before: before, Exists: exists}
	if rel, err := filepath.Rel(repoRoot, t.FilePath); err == nil && !strings.HasPrefix(rel, "..") {
		fc.Rel = filepath.ToSlash(rel)
	} else {
		fc.Rel = filepath.ToSlash(t.FilePath)
	}
	if tool == "Write" {
		fc.After = t.Content
		return fc
	}
	after := before
	for _, r := range t.Replacements() {
		switch {
		case r.OldString != "" && strings.Contains(after, r.OldString):
			n := 1
			if t.ReplaceAll {
				n = -1
			}
			after = strings.Replace(after, r.OldString, r.NewString, n)
		default:
			after += "\n" + r.NewString
		}
	}
	fc.After = after
	return fc
}

// CheckFile runs every file guard over a proposed change.
func CheckFile(cfg config.Config, fc FileChange) Decision {
	return worst(
		protectedConfig(fc),
		lockfile(fc),
		generated(fc),
		envFile(fc),
		secrets(fc),
		suppressions(cfg.Guards, fc),
		skippedTests(fc),
		loweredThresholds(cfg, fc),
	)
}

var protectedNames = map[string]bool{
	config.RepoFile:  true,
	".golangci.yml":  true,
	".golangci.yaml": true,
	".golangci.toml": true,
	".golangci.json": true,
}

// IsProtected reports whether rel (repo-relative, slash-separated) is a
// guardrail config file Claude may not edit: the files above anywhere, and
// settings*.json under a .claude directory.
func IsProtected(rel string) bool {
	base := filepath.Base(rel)
	if protectedNames[base] {
		return true
	}
	if strings.HasPrefix(base, "settings") && strings.HasSuffix(base, ".json") {
		dir := filepath.ToSlash(filepath.Dir(rel))
		return dir == ".claude" || strings.HasSuffix(dir, "/.claude")
	}
	return false
}

func protectedConfig(fc FileChange) Decision {
	const reason = "%s holds standards owned by the human. Propose the change in chat instead of editing it."
	if IsProtected(fc.Rel) {
		return deny("protected-config", fmt.Sprintf(reason, fc.Rel))
	}
	if filepath.Base(fc.Rel) == "pyproject.toml" {
		for _, sec := range []string{"tool.ruff", "tool.mypy", "tool.coverage", "tool.pytest"} {
			if tomlSection(fc.Before, sec) != tomlSection(fc.After, sec) {
				return deny("protected-config", fmt.Sprintf(reason, fc.Rel+" ["+sec+"]"))
			}
		}
	}
	return Allowed
}

// tomlSection returns the text of every table whose name is sec or starts
// with sec + ".".
func tomlSection(content, sec string) string {
	var b strings.Builder
	in := false
	for _, l := range lines(content) {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "[") {
			name := strings.Trim(t, "[] ")
			in = name == sec || strings.HasPrefix(name, sec+".")
		}
		if in {
			b.WriteString(t + "\n")
		}
	}
	return b.String()
}

var lockfiles = map[string]string{
	"go.sum":              "go mod tidy",
	"uv.lock":             "uv lock",
	"poetry.lock":         "poetry lock",
	".terraform.lock.hcl": "terraform init -upgrade",
}

func lockfile(fc FileChange) Decision {
	if cmd, ok := lockfiles[filepath.Base(fc.Rel)]; ok {
		return deny("lockfile", fmt.Sprintf("%s is generated. Run `%s` instead of editing it.", fc.Rel, cmd))
	}
	return Allowed
}

var generatedHeader = regexp.MustCompile(`(?m)^(//|#) Code generated .* DO NOT EDIT\.?$`)

// IsGenerated reports whether content carries a generated-code header in
// its first lines.
func IsGenerated(content string) bool {
	head := content
	if ls := lines(content); len(ls) > 20 {
		head = strings.Join(ls[:20], "\n")
	}
	return generatedHeader.MatchString(head)
}

func generated(fc FileChange) Decision {
	if fc.Exists && IsGenerated(fc.Before) {
		return deny("generated-file", fmt.Sprintf("%s is generated. Edit its source and regenerate.", fc.Rel))
	}
	return Allowed
}

func envFile(fc FileChange) Decision {
	base := filepath.Base(fc.Rel)
	if !strings.HasPrefix(base, ".env") {
		return Allowed
	}
	for _, ok := range []string{".example", ".sample", ".template"} {
		if strings.HasSuffix(base, ok) {
			return Allowed
		}
	}
	return deny("secrets", fmt.Sprintf("%s may hold secrets. Never write secrets to disk; document variables in .env.example.", fc.Rel))
}

// secretPatterns is a focused subset of gitleaks rules with low false-
// positive rates.
var secretPatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{"AWS access key", regexp.MustCompile(`\b(AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{"GitHub token", regexp.MustCompile(`\b(gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{60,})\b`)},
	{"private key", regexp.MustCompile(`-----BEGIN ((RSA|EC|DSA|OPENSSH|PGP|ENCRYPTED) )?PRIVATE KEY( BLOCK)?-----`)},
	{"Slack token", regexp.MustCompile(`\bxox[abprs]-[0-9A-Za-z-]{10,}\b`)},
	{"Stripe live key", regexp.MustCompile(`\b[sr]k_live_[0-9A-Za-z]{20,}\b`)},
	{"Google API key", regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`)},
	{"Anthropic API key", regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,}`)},
	{"OpenAI API key", regexp.MustCompile(`\bsk-(proj-)?[A-Za-z0-9]{32,}\b`)},
}

func secrets(fc FileChange) Decision {
	for i, l := range addedLines(fc.Before, fc.After) {
		for _, p := range secretPatterns {
			if p.re.MatchString(l) {
				return deny("secrets", fmt.Sprintf("new content in %s looks like a %s (added line %d). Never write secrets to disk; read them from the environment.", fc.Rel, p.name, i+1))
			}
		}
	}
	return Allowed
}

var suppressionPatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{"//nolint", regexp.MustCompile(`//\s*nolint\b`)},
	{"# noqa", regexp.MustCompile(`#\s*noqa\b`)},
	{"# type: ignore", regexp.MustCompile(`#\s*type:\s*ignore\b`)},
	{"# pragma: no cover", regexp.MustCompile(`#\s*pragma:\s*no\s*cover\b`)},
	{"//go:build ignore", regexp.MustCompile(`//go:build\s+ignore\b`)},
	{"# tflint-ignore", regexp.MustCompile(`#\s*tflint-ignore\b`)},
	{"# hadolint ignore", regexp.MustCompile(`#\s*hadolint\s+ignore\b`)},
	{"# shellcheck disable", regexp.MustCompile(`#\s*shellcheck\s+disable\b`)},
	{"//lint:ignore", regexp.MustCompile(`//\s*lint:ignore\b`)},
	{"// coverage:ignore", regexp.MustCompile(`//\s*coverage:ignore\b`)},
}

func suppressions(g config.Guards, fc FileChange) Decision {
	tag := g.AllowSuppressionTag
	// Allow tags the human already wrote; Claude adding one is itself a
	// new suppression.
	humanTags := map[string]bool{}
	for _, l := range lines(fc.Before) {
		if strings.Contains(l, tag) {
			humanTags[strings.TrimSpace(l)] = true
		}
	}
	afterLines := lines(fc.After)
	added := addedLines(fc.Before, fc.After)
	for _, l := range added {
		if strings.Contains(l, tag) {
			return deny("suppression", fmt.Sprintf("the %q tag is reserved for the human. Fix the underlying issue instead.", tag))
		}
	}
	for _, l := range added {
		for _, p := range suppressionPatterns {
			if !p.re.MatchString(l) {
				continue
			}
			if precededByHumanTag(afterLines, l, humanTags) {
				continue
			}
			return deny("suppression", fmt.Sprintf("new %s in %s: %q. Fix the issue instead of suppressing it.", p.name, fc.Rel, strings.TrimSpace(l)))
		}
	}
	return Allowed
}

// precededByHumanTag reports whether the line directly above target in
// content is an allow tag the human wrote.
func precededByHumanTag(content []string, target string, humanTags map[string]bool) bool {
	for i, l := range content {
		if l == target && i > 0 && humanTags[strings.TrimSpace(content[i-1])] {
			return true
		}
	}
	return false
}

var (
	skipPatterns = []*regexp.Regexp{
		regexp.MustCompile(`\b[tbf]\.Skip(f|Now)?\(`),
		regexp.MustCompile(`@pytest\.mark\.skip(if)?\b`),
		regexp.MustCompile(`\bpytest\.skip\(`),
		regexp.MustCompile(`@unittest\.skip`),
	}
	xfailPattern = regexp.MustCompile(`@?pytest\.mark\.xfail\b|\bpytest\.xfail\(`)
	issueLink    = regexp.MustCompile(`https?://\S+/(issues|pull)/\d+|\b[A-Z][A-Z0-9]+-\d+\b`)
	shortMode    = regexp.MustCompile(`testing\.Short\(\)`)
)

func skippedTests(fc FileChange) Decision {
	const reason = "new skipped test in %s: %q. Skipping tests to pass the gate isn't allowed; fix the test or the code."
	afterLines := lines(fc.After)
	for _, l := range addedLines(fc.Before, fc.After) {
		for _, re := range skipPatterns {
			if re.MatchString(l) && !guardedByShort(afterLines, l) {
				return deny("skipped-test", fmt.Sprintf(reason, fc.Rel, strings.TrimSpace(l)))
			}
		}
		if xfailPattern.MatchString(l) && !issueLink.MatchString(l) {
			return deny("skipped-test", fmt.Sprintf(reason+" An xfail must link the tracking issue.", fc.Rel, strings.TrimSpace(l)))
		}
	}
	return Allowed
}

// guardedByShort allows the idiomatic `if testing.Short() { t.Skip(...) }`.
func guardedByShort(content []string, target string) bool {
	if shortMode.MatchString(target) {
		return true
	}
	for i, l := range content {
		if l == target && i > 0 && shortMode.MatchString(content[i-1]) {
			return true
		}
	}
	return false
}

var failUnder = regexp.MustCompile(`(?:--cov-fail-under[= ]|fail_under\s*=\s*|-coverage-threshold[= ])(\d+(?:\.\d+)?)`)

func loweredThresholds(cfg config.Config, fc FileChange) Decision {
	before := maxThreshold(fc.Before)
	after := maxThreshold(fc.After)
	if before >= 0 && (after < 0 || after < before) {
		return deny("lowered-threshold", fmt.Sprintf("this edit lowers or removes a coverage threshold in %s. Thresholds are owned by the human; propose the change in chat.", fc.Rel))
	}
	for _, l := range addedLines(fc.Before, fc.After) {
		if m := failUnder.FindStringSubmatch(l); m != nil {
			if v, _ := strconv.ParseFloat(m[1], 64); v < cfg.Python.Coverage.Total {
				return deny("lowered-threshold", fmt.Sprintf("new coverage threshold %s in %s is below the configured %.0f%%.", m[1], fc.Rel, cfg.Python.Coverage.Total))
			}
		}
	}
	return Allowed
}

// maxThreshold returns the highest coverage threshold in content, or -1.
func maxThreshold(content string) float64 {
	best := -1.0
	for _, m := range failUnder.FindAllStringSubmatch(content, -1) {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil && v > best {
			best = v
		}
	}
	return best
}
