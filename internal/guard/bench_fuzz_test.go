package guard

import (
	"path"
	"reflect"
	"strings"
	"testing"

	"github.com/rossedman/guardrails/internal/config"
	"github.com/rossedman/guardrails/internal/hook"
)

var fuzzEnv = Env{
	CurrentBranch: func() string { return "main" },
	KubeContext:   func() string { return "prod-eu" },
}

var bashSeeds = []string{
	"", "ls -la", "A=1 && ls",
	"terraform apply", "terraform -chdir=infra apply", "terraform state list", "terraform plan -auto-approve",
	"cd infra && terraform apply", "/usr/local/bin/terraform apply", "TF_LOG=debug terraform apply",
	"sudo -E terraform apply", "echo $(terraform apply)", "echo `terraform destroy`",
	"docker push ghcr.io/o/i:1", "docker buildx build --output type=registry .", "crane copy a b",
	"git push --force origin feat", "git -C ../other push --force", "git push origin HEAD:main", "git push",
	"git reset --hard HEAD~1", "git commit -nm x", "git commit -am 'msg'", "git clean -fd",
	"kubectl apply --context prod -f x.yaml", "kubectl --context=team-dev apply -f x.yaml", "helm upgrade x ./chart",
	"pytest --cov-fail-under=10", "pytest --cov-fail-under 10",
	"echo hi > .golangci.yml", "tee .guardrails.yaml", "sed -i s/a/b/ .claude/settings.json", "rm .claude/rules/go.md",
	"echo 'a;b' | grep \"c d\" && echo x\\ y\n; (ls)",
	"unterminated 'quote", "trailing backslash \\",
}

func BenchmarkCheckBash(b *testing.B) {
	cfg := config.Default()
	cmds := []string{
		"ls -la",
		"cd internal/guard && go test -race -count=1 ./... 2>&1 | tail -n 40",
		"cat go.mod | grep -v '^//' | awk '{print $1}' | sort -u | head -20 && git status --short && git diff --stat HEAD~1",
		"TF_LOG=debug sudo -E terraform -chdir=infra apply -auto-approve",
		"git push origin HEAD:main && kubectl --context prod-eu apply -f deploy.yaml",
	}
	b.ReportAllocs()
	for b.Loop() {
		for _, c := range cmds {
			CheckBash(cfg, c, fuzzEnv)
		}
	}
}

func BenchmarkSegments(b *testing.B) {
	cmd := "cd \"$REPO/internal pkg\" && for f in *.go; do echo \"$f\" | sed -e 's/a/b/' >> out.txt; done; go vet ./... || (echo failed; exit 1) & wait"
	b.ReportAllocs()
	for b.Loop() {
		if len(Segments(cmd)) == 0 {
			b.Fatal("no segments")
		}
	}
}

func BenchmarkCommands(b *testing.B) {
	cmd := "FOO=1 sudo -E nohup env BAR=2 /usr/bin/terraform plan -out=tf.plan | tee plan.log && time command git status"
	b.ReportAllocs()
	for b.Loop() {
		if len(Commands(cmd)) == 0 {
			b.Fatal("no commands")
		}
	}
}

func FuzzCheckBash(f *testing.F) {
	for _, s := range bashSeeds {
		f.Add(s)
	}
	cfg := config.Default()
	f.Fuzz(func(t *testing.T, cmd string) {
		d := CheckBash(cfg, cmd, fuzzEnv)
		if d2 := CheckBash(cfg, cmd, fuzzEnv); d != d2 {
			t.Fatalf("CheckBash(%q) not deterministic: %+v vs %+v", cmd, d, d2)
		}
		checkDecision(t, d)

		// Segments and Commands never yield empty commands.
		for _, seg := range Segments(cmd) {
			if len(seg) == 0 {
				t.Fatalf("Segments(%q) has an empty segment", cmd)
			}
		}
		for _, args := range Commands(cmd) {
			if len(args) == 0 || args[0] == "" {
				t.Fatalf("Commands(%q) has an empty command: %q", cmd, args)
			}
		}
		if !reflect.DeepEqual(Segments(cmd), Segments(cmd)) {
			t.Fatalf("Segments(%q) not deterministic", cmd)
		}

		// Leading blanks are never significant: nothing is open yet.
		if got := CheckBash(cfg, " \t\n"+cmd, fuzzEnv); got != d {
			t.Fatalf("leading whitespace changes CheckBash(%q): %+v vs %+v", cmd, d, got)
		}
		if !reflect.DeepEqual(Commands(" \t\n"+cmd), Commands(cmd)) {
			t.Fatalf("leading whitespace changes Commands(%q)", cmd)
		}

		// Without quotes or escapes, trailing blanks and newlines are
		// not significant either, and newline joins independent commands.
		if !strings.ContainsAny(cmd, `'"\`) {
			if got := CheckBash(cfg, cmd+" \t\n", fuzzEnv); got != d {
				t.Fatalf("trailing whitespace changes CheckBash(%q): %+v vs %+v", cmd, d, got)
			}
			joined := CheckBash(cfg, cmd+"\nterraform apply", fuzzEnv)
			if joined.Action != hook.Deny {
				t.Fatalf("appending terraform apply after %q: %+v", cmd, joined)
			}
			if severity(joined.Action) < severity(d.Action) {
				t.Fatalf("appending a command lowers severity for %q", cmd)
			}
		}
	})
}

func checkDecision(t *testing.T, d Decision) {
	t.Helper()
	switch d.Action {
	case hook.Allow:
		if d.Guard != "" || d.Reason != "" || d.Message() != "" {
			t.Fatalf("allow decision carries detail: %+v", d)
		}
	case hook.Ask, hook.Deny:
		if d.Guard == "" || d.Reason == "" || d.Message() == "" {
			t.Fatalf("%s decision without guard or reason: %+v", d.Action, d)
		}
	default:
		t.Fatalf("unknown action in %+v", d)
	}
}

func BenchmarkCheckFile(b *testing.B) {
	cfg := config.Default()
	before := strings.Repeat("package x\n\nfunc f() int { return 1 }\n", 300)
	changes := []FileChange{
		NewFileChange("/repo", "Edit", hook.ToolInput{FilePath: "/repo/internal/x/x.go", OldString: "return 1", NewString: "return 2", ReplaceAll: true}, before, true),
		NewFileChange("/repo", "Write", hook.ToolInput{FilePath: "/repo/internal/x/new.go", Content: before}, "", false),
		NewFileChange("/repo", "Edit", hook.ToolInput{FilePath: "/repo/.golangci.yml", OldString: "a", NewString: "b"}, "a\n", true),
		NewFileChange("/repo", "Edit", hook.ToolInput{FilePath: "/repo/x_test.go", OldString: "}", NewString: "t.Skip()\n}"}, before, true),
	}
	b.ReportAllocs()
	for b.Loop() {
		for _, fc := range changes {
			CheckFile(cfg, fc)
		}
	}
}

func BenchmarkNewFileChange(b *testing.B) {
	before := strings.Repeat("line of source code\n", 2000)
	in := hook.ToolInput{FilePath: "/repo/a/b.go", Edits: []hook.Edit{
		{OldString: "line of", NewString: "LINE OF"},
		{OldString: "missing", NewString: "appended"},
	}}
	b.ReportAllocs()
	for b.Loop() {
		NewFileChange("/repo", "MultiEdit", in, before, true)
	}
}

func BenchmarkIsProtected(b *testing.B) {
	rels := []string{"internal/guard/files.go", ".guardrails.yaml", "sub/.claude/settings.local.json", ".claude/rules/go.md", "docs/readme.md"}
	b.ReportAllocs()
	for b.Loop() {
		for _, r := range rels {
			IsProtected(r)
		}
	}
}

func FuzzCheckFile(f *testing.F) {
	type seed struct{ tool, file, old, new, content, before string }
	for _, s := range []seed{
		{"Write", "/repo/a/b.go", "", "", "new", "old"},
		{"Edit", "/repo/b.go", "x", "y", "", "x x"},
		{"Edit", "/repo/b.go", "zzz", "added", "", "body"},
		{"Edit", "/repo/.guardrails.yaml", "version: 1", "mode: warn", "", "version: 1\n"},
		{"Edit", "/repo/.claude/settings.json", "{}", `{"a":1}`, "", "{}"},
		{"Edit", "/repo/.claude/rules/go.md", "a", "b", "", "a\n"},
		{"Edit", "/repo/pyproject.toml", "line-length = 100", "line-length = 200", "", "[tool.ruff]\nline-length = 100\n"},
		{"Write", "/repo/go.sum", "", "", "x", "a"},
		{"Write", "/repo/.env", "", "", "KEY=1", ""},
		{"Write", "/repo/a.go", "", "", "key = \"" + fakeAWS + "\"\n", ""},
		{"Edit", "/repo/a.go", "x", "x //nolint", "", "x\n"},
		{"Edit", "/repo/a_test.go", "}", "t.Skip()\n}", "", "func TestA(t *testing.T) {\n}\n"},
		{"Edit", "/repo/ci.sh", "--cov-fail-under=90", "--cov-fail-under=10", "", "pytest --cov-fail-under=90\n"},
		{"Edit", "/repo/gen.go", "a", "b", "", "// Code generated by x. DO NOT EDIT.\n\npackage a\n"},
		{"Write", "/elsewhere/c.go", "", "", "c", ""},
		{"MultiEdit", "", "", "", "", ""},
	} {
		f.Add(s.tool, s.file, s.old, s.new, s.content, s.before, false, true)
	}
	cfg := config.Default()
	f.Fuzz(func(t *testing.T, tool, file, oldS, newS, content, before string, replaceAll, exists bool) {
		in := hook.ToolInput{FilePath: file, Content: content, OldString: oldS, NewString: newS, ReplaceAll: replaceAll}
		fc := NewFileChange("/repo", tool, in, before, exists)
		if fc.Path != file || fc.Before != before || fc.Exists != exists {
			t.Fatalf("NewFileChange lost inputs: %+v", fc)
		}
		if tool == "Write" && fc.After != content {
			t.Fatalf("Write After = %q, want content %q", fc.After, content)
		}
		if tool != "Write" && len(in.Replacements()) == 0 && fc.After != before {
			t.Fatalf("no replacements but After %q != Before %q", fc.After, before)
		}
		if tool != "Write" && oldS != "" && !strings.Contains(before, oldS) && fc.After != before+"\n"+newS {
			t.Fatalf("missing old string: After = %q", fc.After)
		}
		if fc2 := NewFileChange("/repo", tool, in, before, exists); fc2 != fc {
			t.Fatal("NewFileChange not deterministic")
		}

		d := CheckFile(cfg, fc)
		if d2 := CheckFile(cfg, fc); d != d2 {
			t.Fatalf("CheckFile not deterministic: %+v vs %+v", d, d2)
		}
		checkDecision(t, d)

		// Protected files are denied by the protected-config guard, whatever the content.
		if IsProtected(fc.Rel) && (d.Action != hook.Deny || d.Guard != "protected-config") {
			t.Fatalf("protected %q got %+v", fc.Rel, d)
		}
		// A change that adds nothing cannot trip the content guards.
		same := FileChange{Path: fc.Path, Rel: fc.Rel, Before: before, After: before, Exists: exists}
		switch got := CheckFile(cfg, same); got.Guard {
		case "suppression", "skipped-test", "lowered-threshold":
			t.Fatalf("unchanged content flagged: %+v", got)
		case "secrets":
			if !strings.HasPrefix(path.Base(same.Rel), ".env") {
				t.Fatalf("unchanged content flagged: %+v", got)
			}
		}
	})
}

func FuzzIsProtected(f *testing.F) {
	for _, s := range []string{
		".guardrails.yaml", "svc/.guardrails.yaml", ".claude/settings.json", ".claude/settings.local.json",
		"sub/.claude/settings.json", "config/settings.json", ".golangci.yml", ".golangci.toml",
		".claude/rules/go.md", "a/.claude/rules/x.md", "rules/go.md", "README.md", "", "/", "..", "../.claude/settings.json",
		"a/../.claude/settings.json", ".claude//settings.json", "/.claude/settings.json",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, rel string) {
		got := IsProtected(rel)
		if got != IsProtected(rel) {
			t.Fatalf("IsProtected(%q) not deterministic", rel)
		}
		// Nesting a protected file under a directory keeps it protected.
		if got && !IsProtected("sub/"+rel) {
			t.Fatalf("IsProtected(%q) but not under sub/", rel)
		}
		// A protected name is protected in any directory.
		if protectedNames[path.Base(rel)] && !IsProtected("x/y/"+path.Base(rel)) {
			t.Fatalf("protected name %q not protected when nested", rel)
		}
	})
}
