package guard

import (
	"reflect"
	"strings"
	"testing"

	"github.com/rossedman/guardrails/internal/config"
	"github.com/rossedman/guardrails/internal/hook"
)

func TestCheckBash(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	onMain := Env{CurrentBranch: func() string { return "main" }}
	onFeature := Env{CurrentBranch: func() string { return "feature/x" }}
	prodCtx := Env{KubeContext: func() string { return "prod-eu" }}
	devCtx := Env{KubeContext: func() string { return "staging-dev" }}

	tests := []struct {
		name   string
		cmd    string
		env    Env
		action string
		guard  string
	}{
		{"empty", "", Env{}, hook.Allow, ""},
		{"ls", "ls -la", Env{}, hook.Allow, ""},
		{"assignment only", "A=1 && ls", Env{}, hook.Allow, ""},
		// Terraform.
		{"tf apply", "terraform apply", Env{}, hook.Deny, "terraform-state"},
		{"tf chdir apply", "terraform -chdir=infra apply", Env{}, hook.Deny, "terraform-state"},
		{"tf destroy", "terraform destroy -target=x", Env{}, hook.Deny, "terraform-state"},
		{"tf import", "terraform import aws_s3_bucket.b b", Env{}, hook.Deny, "terraform-state"},
		{"tf state rm", "terraform state rm aws_s3_bucket.b", Env{}, hook.Deny, "terraform-state"},
		{"tf state mv", "terraform state mv a b", Env{}, hook.Deny, "terraform-state"},
		{"tf state list", "terraform state list", Env{}, hook.Allow, ""},
		{"tf state show", "terraform state show a", Env{}, hook.Allow, ""},
		{"tf state bare", "terraform state", Env{}, hook.Allow, ""},
		{"tf auto approve", "terraform plan -auto-approve", Env{}, hook.Deny, "terraform-state"},
		{"tf plan", "terraform plan -out=tf.plan", Env{}, hook.Allow, ""},
		{"tf bare", "terraform", Env{}, hook.Allow, ""},
		{"tf workspace delete", "terraform workspace delete dev", Env{}, hook.Deny, "terraform-state"},
		{"tf workspace list", "terraform workspace list", Env{}, hook.Allow, ""},
		{"tofu apply", "tofu apply", Env{}, hook.Deny, "terraform-state"},
		{"terragrunt destroy", "terragrunt destroy", Env{}, hook.Deny, "terraform-state"},
		{"tf after cd", "cd infra && terraform apply", Env{}, hook.Deny, "terraform-state"},
		{"tf full path", "/usr/local/bin/terraform apply", Env{}, hook.Deny, "terraform-state"},
		{"tf env prefix", "TF_LOG=debug terraform apply", Env{}, hook.Deny, "terraform-state"},
		{"tf sudo -E", "sudo -E terraform apply", Env{}, hook.Deny, "terraform-state"},
		{"tf in subshell", "echo $(terraform apply)", Env{}, hook.Deny, "terraform-state"},
		{"tf in backticks", "echo `terraform destroy`", Env{}, hook.Deny, "terraform-state"},
		// Registries.
		{"docker push", "docker push ghcr.io/o/i:1", Env{}, hook.Ask, "registry-push"},
		{"docker image push", "docker image push i", Env{}, hook.Ask, "registry-push"},
		{"buildx push", "docker buildx build --push -t i .", Env{}, hook.Ask, "registry-push"},
		{"build push", "docker build --push .", Env{}, hook.Ask, "registry-push"},
		{"buildx output registry", "docker buildx build --output type=registry .", Env{}, hook.Ask, "registry-push"},
		{"buildx -o push", "docker buildx build -o type=image,push=true .", Env{}, hook.Ask, "registry-push"},
		{"buildx local", "docker buildx build --output type=local,dest=out .", Env{}, hook.Allow, ""},
		{"docker build", "docker build -t i .", Env{}, hook.Allow, ""},
		{"docker bare", "docker", Env{}, hook.Allow, ""},
		{"podman push", "podman push i", Env{}, hook.Ask, "registry-push"},
		{"crane push", "crane push img.tar ghcr.io/o/i", Env{}, hook.Ask, "registry-push"},
		{"crane copy", "crane copy a b", Env{}, hook.Ask, "registry-push"},
		{"skopeo copy", "skopeo copy docker://a docker://b", Env{}, hook.Ask, "registry-push"},
		{"ko publish", "ko publish ./cmd/x", Env{}, hook.Ask, "registry-push"},
		{"crane digest", "crane digest i", Env{}, hook.Allow, ""},
		// Git.
		{"push force", "git push --force origin feat", onFeature, hook.Deny, "git-history"},
		{"push -f", "git push -f", onFeature, hook.Deny, "git-history"},
		{"push lease", "git push --force-with-lease", onFeature, hook.Deny, "git-history"},
		{"push delete", "git push origin --delete feat", onFeature, hook.Deny, "git-history"},
		{"push refspec plus", "git push origin +feat", onFeature, hook.Deny, "git-history"},
		{"push no-verify", "git push --no-verify origin feat", onFeature, hook.Deny, "git-history"},
		{"push -C force", "git -C ../other push --force", onFeature, hook.Deny, "git-history"},
		{"push -c opt", "git -c core.x=y --no-pager push --force", onFeature, hook.Deny, "git-history"},
		{"push explicit main", "git push origin main", onFeature, hook.Deny, "git-history"},
		{"push HEAD:main", "git push origin HEAD:main", onFeature, hook.Deny, "git-history"},
		{"push refs/heads/master", "git push origin feat:refs/heads/master", onFeature, hook.Deny, "git-history"},
		{"push implicit main", "git push", onMain, hook.Deny, "git-history"},
		{"push origin on main", "git push origin", onMain, hook.Deny, "git-history"},
		{"push HEAD on main", "git push origin HEAD", onMain, hook.Deny, "git-history"},
		{"push feature", "git push -u origin feature/x", onMain, hook.Allow, ""},
		{"push implicit feature", "git push", onFeature, hook.Allow, ""},
		{"push no env", "git push", Env{}, hook.Allow, ""},
		{"reset hard", "git reset --hard HEAD~1", Env{}, hook.Deny, "git-history"},
		{"reset soft", "git reset --soft HEAD~1", Env{}, hook.Allow, ""},
		{"commit no-verify", "git commit --no-verify -m x", Env{}, hook.Deny, "git-history"},
		{"commit -n", "git commit -nm x", Env{}, hook.Deny, "git-history"},
		{"commit -am", "git commit -am 'msg'", Env{}, hook.Allow, ""},
		{"merge no-verify", "git merge --no-verify x", Env{}, hook.Deny, "git-history"},
		{"clean -fd", "git clean -fd", Env{}, hook.Ask, "git-history"},
		{"clean -n", "git clean -n", Env{}, hook.Allow, ""},
		{"git status", "git status", Env{}, hook.Allow, ""},
		{"git only flags", "git --version", Env{}, hook.Allow, ""},
		// Kubernetes.
		{"kubectl apply kind ctx", "kubectl apply --context kind-local -f x.yaml", Env{}, hook.Allow, ""},
		{"kubectl apply ctx eq", "kubectl --context=team-dev apply -f x.yaml", Env{}, hook.Allow, ""},
		{"kubectl apply prod", "kubectl apply --context prod -f x.yaml", Env{}, hook.Ask, "cluster-write"},
		{"kubectl delete current prod", "kubectl delete pod x", prodCtx, hook.Ask, "cluster-write"},
		{"kubectl edit current dev", "kubectl edit deploy x", devCtx, hook.Allow, ""},
		{"kubectl unknown ctx", "kubectl apply -f x.yaml", Env{}, hook.Ask, "cluster-write"},
		{"kubectl get", "kubectl get pods", prodCtx, hook.Allow, ""},
		{"kubectl bare", "kubectl", prodCtx, hook.Allow, ""},
		{"kubectl rollout status", "kubectl rollout status deploy/x", prodCtx, hook.Allow, ""},
		{"kubectl rollout bare", "kubectl rollout", prodCtx, hook.Allow, ""},
		{"kubectl rollout restart", "kubectl rollout restart deploy/x", prodCtx, hook.Ask, "cluster-write"},
		{"helm upgrade", "helm upgrade --install x ./chart --kube-context prod", Env{}, hook.Ask, "cluster-write"},
		{"helm upgrade dev", "helm upgrade x ./chart --kube-context kind-a", Env{}, hook.Allow, ""},
		{"helm template", "helm template ./chart", Env{}, hook.Allow, ""},
		// Thresholds.
		{"cov fail under low", "pytest --cov-fail-under=50", Env{}, hook.Deny, "lowered-threshold"},
		{"cov fail under spaced", "pytest --cov-fail-under 10", Env{}, hook.Deny, "lowered-threshold"},
		{"cov fail under ok", "pytest --cov-fail-under=95", Env{}, hook.Allow, ""},
		{"cov fail under junk", "pytest --cov-fail-under=abc", Env{}, hook.Allow, ""},
		// Protected writes.
		{"redirect guardrails", "echo 'mode: warn' > .guardrails.yaml", Env{}, hook.Deny, "protected-config"},
		{"append golangci", "cat x >> .golangci.yml", Env{}, hook.Deny, "protected-config"},
		{"tee settings", "echo {} | tee .claude/settings.json", Env{}, hook.Deny, "protected-config"},
		{"sed -i", "sed -i 's/90/50/' .guardrails.yaml", Env{}, hook.Deny, "protected-config"},
		{"sed --in-place", "sed --in-place=.bak 's/a/b/' .golangci.yml", Env{}, hook.Deny, "protected-config"},
		{"perl -pi", "perl -pi -e 's/a/b/' .guardrails.yaml", Env{}, hook.Deny, "protected-config"},
		{"rm config", "rm .guardrails.yaml", Env{}, hook.Deny, "protected-config"},
		{"cat config", "cat .guardrails.yaml", Env{}, hook.Allow, ""},
		{"sed print", "sed -n 1p .guardrails.yaml", Env{}, hook.Allow, ""},
		// Worst decision across segments.
		{"ask then deny", "docker push x; terraform apply", Env{}, hook.Deny, "terraform-state"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := CheckBash(cfg, tc.cmd, tc.env)
			if d.Action != tc.action || d.Guard != tc.guard {
				t.Fatalf("CheckBash(%q) = %s/%s (%s), want %s/%s", tc.cmd, d.Action, d.Guard, d.Reason, tc.action, tc.guard)
			}
		})
	}
}

func TestSegments(t *testing.T) {
	t.Parallel()
	tests := []struct {
		cmd  string
		want [][]string
	}{
		{"", nil},
		{"a b", [][]string{{"a", "b"}}},
		{"a && b || c; d | e & f\ng", [][]string{{"a"}, {"b"}, {"c"}, {"d"}, {"e"}, {"f"}, {"g"}}},
		{`echo "a b" 'c d'`, [][]string{{"echo", "a b", "c d"}}},
		{`echo "x \"y\" \z"`, [][]string{{"echo", `x "y" z`}}},
		{`echo 'it''s'`, [][]string{{"echo", "its"}}},
		{`echo a\ b`, [][]string{{"echo", "a b"}}},
		{"a \\\nb", [][]string{{"a", "b"}}},
		{`echo ""`, [][]string{{"echo", ""}}},
		{"x $(y z) w", [][]string{{"x"}, {"y", "z"}, {"w"}}},
		{"x `y` w", [][]string{{"x"}, {"y"}, {"w"}}},
		{"(cd a; b)", [][]string{{"cd", "a"}, {"b"}}},
		{"echo a>f", [][]string{{"echo", "a", ">", "f"}}},
		{"echo a >> f < g", [][]string{{"echo", "a", ">>", "f", "<", "g"}}},
		{"a\tb", [][]string{{"a", "b"}}},
		{"$HOME", [][]string{{"$HOME"}}},
	}
	for _, tc := range tests {
		got := Segments(tc.cmd)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Segments(%q) = %q, want %q", tc.cmd, got, tc.want)
		}
	}
}

func TestStripPrefixes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   []string
		want []string
	}{
		{[]string{"sudo", "-E", "env", "A=1", "B=2", "/bin/terraform", "apply"}, []string{"terraform", "apply"}},
		{[]string{"nice", "nohup", "time", "exec", "command", "ls"}, []string{"ls"}},
		{[]string{"A=1"}, nil},
		{[]string{"-x", "y"}, []string{"-x", "y"}},
	}
	for _, tc := range tests {
		got := stripPrefixes(tc.in)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("stripPrefixes(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	in := []string{"/usr/bin/git", "push"}
	_ = stripPrefixes(in)
	if in[0] != "/usr/bin/git" {
		t.Error("stripPrefixes mutated its input")
	}
}

func TestRulesFilesProtected(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	for cmd, want := range map[string]string{
		"echo '- always wrap errors' >> .claude/rules/go.md": hook.Deny,
		"sed -i 's/x/y/' .claude/rules/go.md":                hook.Deny,
		"cat .claude/rules/go.md":                            hook.Allow,
	} {
		if d := CheckBash(cfg, cmd, Env{}); d.Action != want {
			t.Errorf("CheckBash(%q) = %+v, want %s", cmd, d, want)
		}
	}
	fc := NewFileChange("/r", "Edit", hook.ToolInput{FilePath: "/r/.claude/rules/go.md", OldString: "a", NewString: "b"}, "a\n", true)
	if d := CheckFile(cfg, fc); d.Action != hook.Deny || d.Guard != "protected-config" {
		t.Errorf("edit rules file = %+v", d)
	}
}

func TestIsProtected(t *testing.T) {
	t.Parallel()
	for rel, want := range map[string]bool{
		".guardrails.yaml":             true,
		"a/b/.golangci.yml":            true,
		".claude/settings.json":        true,
		".claude/settings.local.json":  true,
		"x/.claude/settings.json":      true,
		".claude/agents/settings.json": false,
		"settings.json":                false,
		".claude/rules/go.md":          true,
		"svc/.claude/rules/python.md":  true,
		".claude/rules/notes.txt":      false,
		"docs/rules/go.md":             false,
		".claude/agents/go.md":         false,
		"main.go":                      false,
	} {
		if got := IsProtected(rel); got != want {
			t.Errorf("IsProtected(%q) = %v, want %v", rel, got, want)
		}
	}
}

func TestCommands(t *testing.T) {
	got := Commands(`FOO=1 sudo npm install left-pad && ; echo "a b"`)
	if len(got) != 2 || strings.Join(got[0], " ") != "npm install left-pad" || got[1][1] != "a b" {
		t.Errorf("Commands = %q", got)
	}
	if d := Worst(Allowed, ask("a", "x"), deny("b", "y"), deny("c", "z")); d.Guard != "b" {
		t.Errorf("Worst = %+v", d)
	}
}
