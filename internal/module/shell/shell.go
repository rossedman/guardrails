// Package shell is the shell script module: shfmt on edit, shellcheck on
// the edited script, and a Stop gate of shellcheck plus a formatting check
// over every touched script.
package shell

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rossedman/guardrails/internal/config"
	"github.com/rossedman/guardrails/internal/feedback"
	"github.com/rossedman/guardrails/internal/module"
)

// Module implements module.Module for shell scripts.
type Module struct{}

// New returns the shell module.
func New() *Module { return &Module{} }

// Name implements module.Module.
func (*Module) Name() string { return "shell" }

// Enabled implements module.Module.
func (*Module) Enabled(cfg config.Config) bool { return cfg.ShellEnabled() }

// Match implements module.Module. Scripts are matched by extension, or for
// extensionless files by a sh/bash/dash/ksh shebang.
func (*Module) Match(path string) bool {
	switch filepath.Ext(path) {
	case ".sh", ".bash", ".ksh":
		return true
	case "":
		return shebang(path)
	default:
		return false
	}
}

var shells = map[string]bool{"sh": true, "bash": true, "dash": true, "ksh": true}

// shebang reports whether the file starts with a shell shebang, reading at
// most 256 bytes.
func shebang(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, 256)
	n, _ := f.Read(buf)
	line, _, _ := bytes.Cut(buf[:n], []byte("\n"))
	rest, ok := bytes.CutPrefix(line, []byte("#!"))
	if !ok {
		return false
	}
	fields := strings.Fields(string(rest))
	if len(fields) == 0 {
		return false
	}
	interp := filepath.Base(fields[0])
	if interp == "env" {
		args := fields[1:]
		for len(args) > 0 && strings.HasPrefix(args[0], "-") {
			args = args[1:]
		}
		if len(args) == 0 {
			return false
		}
		interp = filepath.Base(args[0])
	}
	return shells[interp]
}

// Tools implements module.Module.
func (*Module) Tools() []module.Tool {
	return []module.Tool{
		{Name: "shellcheck", Required: true, Install: "managed, or https://github.com/koalaman/shellcheck#installing", VersionArgs: []string{"--version"}},
		{Name: "shfmt", Required: true, Install: "managed, or go install mvdan.cc/sh/v3/cmd/shfmt@latest", VersionArgs: []string{"--version"}},
	}
}

// tool returns the module's declared tool called name, or a bare one if there is none.
func (m *Module) tool(name string) module.Tool {
	for _, t := range m.Tools() {
		if t.Name == name {
			return t
		}
	}
	return module.Tool{Name: name}
}

// Standards implements module.Module.
func (*Module) Standards(cfg config.Config) string {
	return fmt.Sprintf("Shell standards: shfmt formats every script you edit; shellcheck (severity %s and above) runs on each edit, and before a turn can end every touched script must be shellcheck-clean and shfmt-formatted.", cfg.Shell.Severity)
}

// Fix implements module.Module.
func (m *Module) Fix(ctx context.Context, env module.Env, files []string) module.FixResult {
	var res module.FixResult
	scripts := existing(files)
	if len(scripts) == 0 {
		return res
	}
	if _, err := env.RunTool(ctx, env.Repo, "shfmt", append([]string{"-w"}, scripts...)...); module.IsUnavailable(err) {
		res.Notes = append(res.Notes, module.Unavailable(m.tool("shfmt"), err)+"; scripts were not formatted")
	}
	return res
}

// Check implements module.Module.
func (m *Module) Check(ctx context.Context, env module.Env, files []string) []feedback.Check {
	scripts := existing(files)
	if len(scripts) == 0 {
		return nil
	}
	c, failed, err := m.shellcheck(ctx, env, scripts)
	switch {
	case module.IsUnavailable(err):
		// Unavailable tools are reported at SessionStart and fail the gate;
		// an edit is never blocked on them.
		return nil
	case err != nil:
		return []feedback.Check{startFailure(err)}
	case failed:
		return []feedback.Check{c}
	}
	return nil
}

// Gate implements module.Module.
func (m *Module) Gate(ctx context.Context, env module.Env, in module.GateInput) module.GateResult {
	res := module.GateResult{Total: -1}
	files := in.Files
	if in.All {
		files = m.all(env.Repo)
	}
	scripts := existing(files)
	if len(scripts) == 0 {
		return res
	}

	res.Ran = append(res.Ran, "shellcheck")
	c, failed, err := m.shellcheck(ctx, env, scripts)
	switch {
	case module.IsUnavailable(err):
		res.Checks = append(res.Checks, m.unavailable("shellcheck", err))
	case err != nil:
		res.Checks = append(res.Checks, startFailure(err))
	case failed:
		res.Checks = append(res.Checks, c)
	}

	res.Ran = append(res.Ran, "format")
	out, err := env.RunTool(ctx, env.Repo, "shfmt", append([]string{"-l"}, scripts...)...)
	switch {
	case module.IsUnavailable(err):
		res.Checks = append(res.Checks, m.unavailable("shfmt", err))
	case err != nil:
		res.Notes = append(res.Notes, "shfmt could not run: "+err.Error())
	default:
		var unformatted []string
		for _, l := range strings.Split(strings.TrimSpace(out.Stdout), "\n") {
			if l = strings.TrimSpace(l); l != "" {
				unformatted = append(unformatted, rel(env.Repo, l))
			}
		}
		if len(unformatted) > 0 {
			res.Checks = append(res.Checks, feedback.Check{
				Name: "format", Failed: true,
				Summary: fmt.Sprintf("%d script%s not shfmt-formatted", len(unformatted), plural(len(unformatted))),
				Lines:   unformatted,
				Fix:     "run `shfmt -w` on the files above",
			})
		} else if out.ExitCode != 0 {
			res.Notes = append(res.Notes, "shfmt failed: "+firstLine(out.Combined()))
		}
	}
	return res
}

// unavailable is the failed Check for a tool that is missing and cannot be installed.
func (m *Module) unavailable(name string, err error) feedback.Check {
	return feedback.Check{
		Name: "tools", Failed: true,
		Summary: module.Unavailable(m.tool(name), err),
		Fix:     "the human must install " + name + " or set tools.install: auto",
	}
}

// startFailure is the failed Check for shellcheck failing to start.
func startFailure(err error) feedback.Check {
	return feedback.Check{Name: "shellcheck", Failed: true, Summary: "could not run: " + err.Error()}
}

type shellcheckReport struct {
	Comments []struct {
		File    string `json:"file"`
		Line    int    `json:"line"`
		Column  int    `json:"column"`
		Level   string `json:"level"`
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"comments"`
}

// shellcheck runs shellcheck over scripts. It returns the failing check and
// true when there are issues, or an error when the tool is unavailable or
// could not start. Output that cannot be parsed is a failing check.
func (m *Module) shellcheck(ctx context.Context, env module.Env, scripts []string) (feedback.Check, bool, error) {
	args := append([]string{"-f", "json1", "-S", env.Config.Shell.Severity}, scripts...)
	res, err := env.RunTool(ctx, env.Repo, "shellcheck", args...)
	if module.IsUnavailable(err) {
		return feedback.Check{}, false, err
	}
	if err != nil {
		return feedback.Check{}, false, err
	}
	var rep shellcheckReport
	if jerr := json.Unmarshal([]byte(strings.TrimSpace(res.Stdout)), &rep); jerr != nil {
		if res.ExitCode == 0 {
			return feedback.Check{}, false, nil
		}
		return feedback.Check{
			Name: "shellcheck", Failed: true, Summary: "shellcheck failed to run",
			Lines: []string{firstLine(res.Combined())},
		}, true, nil
	}
	want := map[string]bool{}
	for _, s := range scripts {
		want[filepath.Clean(s)] = true
	}
	var issues []feedback.Issue
	for _, c := range rep.Comments {
		p := c.File
		if !filepath.IsAbs(p) {
			p = filepath.Join(env.Repo, p)
		}
		if !want[filepath.Clean(p)] {
			continue
		}
		issues = append(issues, feedback.Issue{
			File: rel(env.Repo, p), Line: c.Line, Col: c.Column,
			Rule: fmt.Sprintf("SC%d", c.Code), Message: c.Message,
		})
	}
	if len(issues) == 0 {
		return feedback.Check{}, false, nil
	}
	return feedback.Check{
		Name: "shellcheck", Failed: true,
		Summary: fmt.Sprintf("%d issue%s", len(issues), plural(len(issues))),
		Issues:  issues,
		Fix:     "fix each issue; `# shellcheck disable` comments are not allowed",
	}, true, nil
}

// all lists every script in the repo, skipping hidden, vendor and
// node_modules directories.
func (m *Module) all(repo string) []string {
	var out []string
	_ = filepath.WalkDir(repo, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return skipUnreadable(d)
		}
		if d.IsDir() {
			name := d.Name()
			if p != repo && (strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if m.Match(p) {
			out = append(out, p)
		}
		return nil
	})
	return out
}

// existing keeps files that still exist.
func existing(files []string) []string {
	var out []string
	for _, f := range files {
		if info, err := os.Stat(f); err == nil && !info.IsDir() {
			out = append(out, f)
		}
	}
	return out
}

// rel returns p relative to repo with forward slashes, or p unchanged if it is relative or outside repo.
func rel(repo, p string) string {
	if !filepath.IsAbs(p) {
		return filepath.ToSlash(p)
	}
	if r, err := filepath.Rel(repo, p); err == nil && !strings.HasPrefix(r, "..") {
		return filepath.ToSlash(r)
	}
	return filepath.ToSlash(p)
}

// plural returns "s" unless n is 1.
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// firstLine returns the first line of s.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// skipUnreadable skips an unreadable directory and ignores an unreadable
// file, so one bad entry never stops the walk.
func skipUnreadable(d os.DirEntry) error {
	if d != nil && d.IsDir() {
		return filepath.SkipDir
	}
	return nil
}
