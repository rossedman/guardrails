package guard

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rossedman/guardrails/internal/config"
	"github.com/rossedman/guardrails/internal/glob"
)

// Env supplies facts about the environment that Bash guards need. Either
// function may be nil, in which case the guard assumes the cautious answer.
type Env struct {
	// CurrentBranch returns the checked-out git branch.
	CurrentBranch func() string
	// KubeContext returns the current kubectl context.
	KubeContext func() string
}

// CheckBash runs every Bash guard over a command line.
func CheckBash(cfg config.Config, command string, env Env) Decision {
	var ds []Decision
	for _, seg := range Segments(command) {
		args := stripPrefixes(seg)
		if len(args) == 0 {
			continue
		}
		ds = append(ds,
			terraformGuard(args),
			registryGuard(args),
			gitGuard(cfg.Guards, args, env),
			kubectlGuard(cfg.Guards, args, env),
			thresholdGuard(cfg, args),
			protectedWriteGuard(seg),
		)
	}
	return worst(ds...)
}

// Segments splits a shell command into simple commands on ;, &&, ||, |, &
// and newlines, tokenizing each with basic quote handling. It is not a full
// shell parser; it errs toward splitting more, which only makes guards see
// more commands.
func Segments(command string) [][]string {
	var (
		segs [][]string
		cur  []string
		tok  strings.Builder
		has  bool
		q    rune
	)
	flushTok := func() {
		if has {
			cur = append(cur, tok.String())
			tok.Reset()
			has = false
		}
	}
	flushSeg := func() {
		flushTok()
		if len(cur) > 0 {
			segs = append(segs, cur)
			cur = nil
		}
	}
	rs := []rune(command)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case q != 0 && r == q:
			q = 0
		case q == '"' && r == '\\' && i+1 < len(rs):
			i++
			tok.WriteRune(rs[i])
		case q != 0:
			tok.WriteRune(r)
		case r == '\'' || r == '"':
			q, has = r, true
		case r == '\\' && i+1 < len(rs):
			i++
			if rs[i] != '\n' {
				tok.WriteRune(rs[i])
				has = true
			}
		case r == ';' || r == '|' || r == '&' || r == '\n' || r == '(' || r == ')' || r == '`':
			flushSeg()
		case r == '$' && i+1 < len(rs) && rs[i+1] == '(':
			flushSeg()
			i++
		case r == '>' || r == '<':
			flushTok()
			op := string(r)
			if i+1 < len(rs) && rs[i+1] == '>' {
				op += ">"
				i++
			}
			cur = append(cur, op)
		case r == ' ' || r == '\t':
			flushTok()
		default:
			tok.WriteRune(r)
			has = true
		}
	}
	flushSeg()
	return segs
}

// stripPrefixes drops leading env assignments and wrappers such as sudo,
// env, command, exec, time and nohup.
func stripPrefixes(args []string) []string {
	wrappers := map[string]bool{"sudo": true, "env": true, "command": true, "exec": true, "time": true, "nohup": true, "nice": true}
	out := append([]string(nil), args...)
	afterWrapper := false
	for len(out) > 0 {
		a := out[0]
		switch {
		case wrappers[a]:
			afterWrapper = true
		case afterWrapper && strings.HasPrefix(a, "-"):
			// flags to a wrapper, such as `sudo -E`
		case strings.Contains(a, "=") && !strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "="):
			// VAR=value assignment
		default:
			out[0] = filepath.Base(a)
			return out
		}
		out = out[1:]
	}
	return nil
}

// positional returns arguments that are not flags.
func positional(args []string) []string {
	var out []string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			out = append(out, a)
		}
	}
	return out
}

func hasFlag(args []string, names ...string) bool {
	for _, a := range args {
		for _, n := range names {
			if a == n || strings.HasPrefix(a, n+"=") {
				return true
			}
		}
	}
	return false
}

func flagValue(args []string, name string) (string, bool) {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1], true
		}
		if strings.HasPrefix(a, name+"=") {
			return strings.TrimPrefix(a, name+"="), true
		}
	}
	return "", false
}

func terraformGuard(args []string) Decision {
	if args[0] != "terraform" && args[0] != "tofu" && args[0] != "terragrunt" {
		return Allowed
	}
	const reason = "`%s` changes real infrastructure or state. Run `%s plan` and hand the plan to the human; they apply."
	pos := positional(args[1:])
	if hasFlag(args, "-auto-approve", "--auto-approve") {
		return deny("terraform-state", fmt.Sprintf(reason, strings.Join(args, " "), args[0]))
	}
	if len(pos) == 0 {
		return Allowed
	}
	switch pos[0] {
	case "apply", "destroy", "import", "taint", "untaint", "force-unlock":
		return deny("terraform-state", fmt.Sprintf(reason, args[0]+" "+pos[0], args[0]))
	case "state":
		if len(pos) > 1 && pos[1] != "list" && pos[1] != "show" && pos[1] != "pull" {
			return deny("terraform-state", fmt.Sprintf(reason, args[0]+" state "+pos[1], args[0]))
		}
	case "workspace":
		if len(pos) > 1 && pos[1] == "delete" {
			return deny("terraform-state", fmt.Sprintf(reason, args[0]+" workspace delete", args[0]))
		}
	}
	return Allowed
}

func registryGuard(args []string) Decision {
	const reason = "pushing images to a registry is the human's call. Build and test locally; ask before `%s`."
	pos := positional(args[1:])
	switch args[0] {
	case "docker", "podman", "nerdctl":
		if len(pos) > 0 && pos[0] == "push" {
			return ask("registry-push", fmt.Sprintf(reason, args[0]+" push"))
		}
		if len(pos) > 1 && pos[0] == "image" && pos[1] == "push" {
			return ask("registry-push", fmt.Sprintf(reason, args[0]+" image push"))
		}
		if len(pos) > 0 && (pos[0] == "buildx" || pos[0] == "build") && hasFlag(args, "--push") {
			return ask("registry-push", fmt.Sprintf(reason, args[0]+" "+pos[0]+" --push"))
		}
		if len(pos) > 0 && pos[0] == "buildx" && hasOutputPush(args) {
			return ask("registry-push", fmt.Sprintf(reason, "docker buildx --output type=registry"))
		}
	case "crane", "skopeo", "oras", "ko":
		if len(pos) > 0 && (pos[0] == "push" || pos[0] == "copy" || pos[0] == "cp" || pos[0] == "publish") {
			return ask("registry-push", fmt.Sprintf(reason, args[0]+" "+pos[0]))
		}
	}
	return Allowed
}

func hasOutputPush(args []string) bool {
	for _, flag := range []string{"--output", "-o"} {
		if v, ok := flagValue(args, flag); ok && (strings.Contains(v, "type=registry") || strings.Contains(v, "push=true")) {
			return true
		}
	}
	return false
}

func gitGuard(g config.Guards, args []string, env Env) Decision {
	if args[0] != "git" {
		return Allowed
	}
	// Skip global options such as `git -C dir`.
	rest := args[1:]
	for len(rest) > 0 && strings.HasPrefix(rest[0], "-") {
		if (rest[0] == "-C" || rest[0] == "-c") && len(rest) > 1 {
			rest = rest[2:]
			continue
		}
		rest = rest[1:]
	}
	if len(rest) == 0 {
		return Allowed
	}
	sub, subArgs := rest[0], rest[1:]
	const reason = "%s rewrites shared history or skips hooks. Leave that to the human."
	switch sub {
	case "push":
		if hasFlag(subArgs, "--force", "-f", "--force-with-lease", "--force-if-includes", "--mirror", "--delete", "-d") {
			return deny("git-history", fmt.Sprintf(reason, "`git push` with force/delete"))
		}
		if hasFlag(subArgs, "--no-verify") {
			return deny("git-history", fmt.Sprintf(reason, "`git push --no-verify`"))
		}
		return pushTarget(g, positional(subArgs), env)
	case "reset":
		if hasFlag(subArgs, "--hard") {
			return deny("git-history", fmt.Sprintf(reason, "`git reset --hard`"))
		}
	case "commit", "merge", "rebase", "cherry-pick", "revert", "am":
		if hasFlag(subArgs, "--no-verify") || (sub == "commit" && hasShortFlag(subArgs, 'n')) {
			return deny("git-history", fmt.Sprintf(reason, "`git "+sub+" --no-verify`"))
		}
	case "clean":
		if hasShortFlag(subArgs, 'f') {
			return ask("git-history", "`git clean -f` deletes untracked files permanently.")
		}
	}
	return Allowed
}

// hasShortFlag reports whether a combined short flag group such as -an
// contains c.
func hasShortFlag(args []string, c rune) bool {
	for _, a := range args {
		if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.ContainsRune(a[1:], c) {
			return true
		}
	}
	return false
}

func pushTarget(g config.Guards, pos []string, env Env) Decision {
	protected := func(ref string) bool {
		ref = strings.TrimPrefix(ref, "+")
		if i := strings.LastIndex(ref, ":"); i >= 0 {
			ref = ref[i+1:]
		}
		ref = strings.TrimPrefix(ref, "refs/heads/")
		for _, b := range g.ProtectedBranches {
			if ref == b {
				return true
			}
		}
		return false
	}
	for _, p := range pos {
		if strings.HasPrefix(p, "+") {
			return deny("git-history", "a `+refspec` push is a force push. Leave that to the human.")
		}
	}
	msg := func(b string) Decision {
		return deny("git-history", fmt.Sprintf("pushing to protected branch %q. Push a feature branch and open a pull request.", b))
	}
	if len(pos) >= 2 {
		for _, ref := range pos[1:] {
			if protected(ref) {
				return msg(ref)
			}
		}
		if pos[1] != "HEAD" {
			return Allowed
		}
	}
	// No refspec (or HEAD): the current branch is pushed.
	if env.CurrentBranch == nil {
		return Allowed
	}
	if b := env.CurrentBranch(); protected(b) {
		return msg(b)
	}
	return Allowed
}

func kubectlGuard(g config.Guards, args []string, env Env) Decision {
	if args[0] != "kubectl" && args[0] != "oc" && args[0] != "helm" {
		return Allowed
	}
	writes := map[string]bool{
		"apply": true, "delete": true, "edit": true, "patch": true, "replace": true,
		"scale": true, "create": true, "drain": true, "cordon": true, "rollout": true,
		"install": true, "upgrade": true, "uninstall": true, "rollback": true,
	}
	pos := positional(args[1:])
	if len(pos) == 0 || !writes[pos[0]] {
		return Allowed
	}
	if args[0] == "kubectl" && pos[0] == "rollout" && (len(pos) < 2 || pos[1] == "status" || pos[1] == "history") {
		return Allowed
	}
	ctx, ok := flagValue(args, "--context")
	if !ok {
		ctx, ok = flagValue(args, "--kube-context")
	}
	if !ok && env.KubeContext != nil {
		ctx = env.KubeContext()
	}
	for _, safe := range g.KubectlSafeContexts {
		if ctx != "" && glob.Match(safe, ctx) {
			return Allowed
		}
	}
	if ctx == "" {
		ctx = "unknown"
	}
	return ask("cluster-write", fmt.Sprintf("`%s %s` against context %q, which is not a dev cluster. Non-dev clusters need the human.", args[0], pos[0], ctx))
}

func thresholdGuard(cfg config.Config, args []string) Decision {
	for i, a := range args {
		var raw string
		switch {
		case strings.HasPrefix(a, "--cov-fail-under="):
			raw = strings.TrimPrefix(a, "--cov-fail-under=")
		case a == "--cov-fail-under" && i+1 < len(args):
			raw = args[i+1]
		default:
			continue
		}
		if v, err := strconv.ParseFloat(raw, 64); err == nil && v < cfg.Python.Coverage.Total {
			return deny("lowered-threshold", fmt.Sprintf("--cov-fail-under=%s is below the configured %.0f%%. Thresholds are owned by the human.", raw, cfg.Python.Coverage.Total))
		}
	}
	return Allowed
}

// protectedWriteGuard catches shell writes to guardrail config files, which
// would otherwise bypass the Edit/Write guards.
func protectedWriteGuard(seg []string) Decision {
	writers := map[string]bool{"tee": true, "rm": true, "mv": true, "cp": true, "truncate": true, "dd": true, "ln": true, "chmod": true}
	args := stripPrefixes(seg)
	writes := len(args) > 0 && (writers[args[0]] || (args[0] == "sed" && hasInPlace(args)) || (args[0] == "perl" && hasInPlace(args)))
	for i, a := range seg {
		redirected := i > 0 && (seg[i-1] == ">" || seg[i-1] == ">>")
		if (writes || redirected) && IsProtected(filepath.ToSlash(a)) {
			return deny("protected-config", fmt.Sprintf("%s holds standards owned by the human. Propose the change in chat instead of editing it.", a))
		}
	}
	return Allowed
}

func hasInPlace(args []string) bool {
	for _, a := range args[1:] {
		if a == "--in-place" || strings.HasPrefix(a, "--in-place=") {
			return true
		}
	}
	// -i alone, with a suffix (-i.bak) or grouped (-Ei, perl -pi).
	return hasShortFlag(args[1:], 'i')
}
