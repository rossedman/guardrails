package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rossedman/guardrails/internal/config"
	"github.com/rossedman/guardrails/internal/deps"
	"github.com/rossedman/guardrails/internal/hook"
	"github.com/rossedman/guardrails/internal/lessons"
	"github.com/rossedman/guardrails/internal/module"
)

// withRegistry points the dependency check at a fake deps.dev where no
// package exists and OSV knows of no advisories.
func (h *harness) withRegistry() {
	h.t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/query") {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		http.NotFound(w, r)
	}))
	h.t.Cleanup(srv.Close)
	c := deps.NewChecker("")
	c.DepsDevURL, c.OSVURL = srv.URL, srv.URL
	h.eng.Deps = c
}

func TestPreToolUseChecksDependencies(t *testing.T) {
	h := newHarness(t)
	h.withRegistry()
	bash := func(cmd string) hook.Output {
		t.Helper()
		out, err := h.eng.Handle(context.Background(), hook.PreToolUse, h.in(hook.PreToolUse, map[string]any{"tool_name": "Bash", "tool_input": map[string]any{"command": cmd}}))
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	out := bash("npm install lodahs-not-real")
	if out.Specific == nil || out.Specific.PermissionDecision != hook.Deny || !strings.Contains(out.Specific.PermissionDecisionReason, "guardrails [deps]") {
		t.Fatalf("hallucinated package: %+v", out.Specific)
	}
	if out := bash("ls -la"); !out.IsZero() {
		t.Errorf("no packages: %+v", out)
	}
	// A guard's deny wins without lookups.
	if out := bash("terraform destroy && npm install lodahs-not-real"); !strings.Contains(out.Specific.PermissionDecisionReason, "terraform") {
		t.Errorf("guard deny first: %+v", out.Specific)
	}

	edit := func() hook.Output {
		t.Helper()
		p := h.write("package.json", `{"dependencies": {}}`)
		out, err := h.eng.Handle(context.Background(), hook.PreToolUse, h.in(hook.PreToolUse, map[string]any{"tool_name": "Write", "tool_input": map[string]any{"file_path": p, "content": `{"dependencies": {"lodahs-not-real": "^1.0.0"}}`}}))
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if out := edit(); out.Specific == nil || out.Specific.PermissionDecision != hook.Deny {
		t.Errorf("manifest edit: %+v", out.Specific)
	}

	h.config("deps:\n  enabled: false\n")
	if out := bash("npm install lodahs-not-real"); !out.IsZero() {
		t.Errorf("disabled: %+v", out)
	}

	// Allow-listed packages need no lookups, so the default checker is safe
	// to build here.
	h.config("deps:\n  allow: ['*']\n")
	h.eng.Deps = nil
	if out := bash("npm install lodahs-not-real"); !out.IsZero() {
		t.Errorf("allow-listed: %+v", out)
	}
}

func TestUserPromptSubmitRecordsLessons(t *testing.T) {
	h := newHarness(t)
	prompt := func(text string) hook.Output {
		t.Helper()
		out, err := h.eng.Handle(context.Background(), hook.UserPromptSubmit, h.in(hook.UserPromptSubmit, map[string]any{"prompt": text}))
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	recorded := func() int {
		t.Helper()
		ls, _, err := lessons.Load(h.data)
		if err != nil {
			t.Fatal(err)
		}
		return len(ls)
	}

	if out := prompt("Don't mock the database."); !out.IsZero() || recorded() != 0 {
		t.Errorf("a first prompt has nothing to correct: %+v", out)
	}
	out := prompt("No, don't mock the database; use the test container.")
	if out.Specific == nil || out.Specific.HookEventName != hook.UserPromptSubmit || out.Specific.AdditionalContext == "" || recorded() != 1 {
		t.Fatalf("correction: %+v (recorded %d)", out.Specific, recorded())
	}
	if out := prompt("Add a health check endpoint."); !out.IsZero() || recorded() != 1 {
		t.Errorf("not a correction: %+v", out)
	}

	h.config("lessons:\n  nudge: false\n")
	if out := prompt("Stop adding comments to every line."); !out.IsZero() || recorded() != 2 {
		t.Errorf("no nudge: %+v (recorded %d)", out, recorded())
	}
	h.config("lessons:\n  enabled: false\n")
	if out := prompt("Never commit straight to main."); !out.IsZero() || recorded() != 2 {
		t.Errorf("disabled: %+v", out)
	}
	h.config("bogus: true\n")
	if out := prompt("Never commit straight to main."); !out.IsZero() {
		t.Errorf("bad config: %+v", out)
	}
}

func TestUserPromptSubmitExplicitFirstPrompt(t *testing.T) {
	h := newHarness(t)
	out, err := h.eng.Handle(context.Background(), hook.UserPromptSubmit, h.in(hook.UserPromptSubmit, map[string]any{"prompt": "From now on, always run make lint before you stop."}))
	if err != nil || out.Specific == nil {
		t.Fatalf("explicit standing rule: %+v %v", out, err)
	}

	// The next session hears about it.
	out, err = h.eng.Handle(context.Background(), hook.SessionStart, h.in(hook.SessionStart, map[string]any{"session_id": "s2", "source": "startup"}))
	if err != nil || !strings.Contains(out.SystemMessage, "1 correction(s) recorded; run /guardrails:learn") {
		t.Errorf("session start hint: %q %v", out.SystemMessage, err)
	}
	h.mod.enabled = false
	out, err = h.eng.Handle(context.Background(), hook.SessionStart, h.in(hook.SessionStart, map[string]any{"session_id": "s3", "source": "startup"}))
	if err != nil || out.Specific != nil || !strings.Contains(out.SystemMessage, "/guardrails:learn") {
		t.Errorf("hint without active modules: %+v %v", out, err)
	}
}

func TestSessionStartWelcomesOnce(t *testing.T) {
	h := newHarness(t)
	start := func(id string) hook.Output {
		t.Helper()
		out, err := h.eng.Handle(context.Background(), hook.SessionStart, h.in(hook.SessionStart, map[string]any{"session_id": id, "source": "startup"}))
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if out := start("a"); !strings.Contains(out.SystemMessage, "on in this repo for Stub · strict ·") {
		t.Fatalf("first session: %q", out.SystemMessage)
	}
	if out := start("b"); strings.Contains(out.SystemMessage, "on in this repo") {
		t.Errorf("welcomed twice: %q", out.SystemMessage)
	}
}

func TestWelcomeAndBar(t *testing.T) {
	cfg := config.Default()
	goMod, sh := &named{name: "go"}, &named{name: "shell"}
	if got := Welcome(cfg, []module.Module{sh, goMod}); !strings.Contains(got, "for Shell, Go · strict (90% coverage, 90% of changed lines) ·") {
		t.Errorf("Welcome = %q", got)
	}
	if err := config.ApplyPreset(&cfg, config.PresetAdopt); err != nil {
		t.Fatal(err)
	}
	if got := Bar(cfg, "python"); got != "coverage may not drop, 80% of changed lines" {
		t.Errorf("Bar(python) = %q", got)
	}
	if got := Welcome(cfg, []module.Module{sh}); !strings.Contains(got, "for Shell · adopt ·") {
		t.Errorf("Welcome without coverage = %q", got)
	}
	for in, want := range map[string]string{"go": "Go", "yaml": "YAML", "terraform": "Terraform", "": ""} {
		if got := DisplayName(in); got != want {
			t.Errorf("DisplayName(%q) = %q", in, got)
		}
	}
}

// named is a module with only a name, for the display helpers.
type named struct {
	stub
	name string
}

func (n *named) Name() string { return n.name }

func TestPresentIgnoresOwnConfig(t *testing.T) {
	h := newHarness(t)
	yamlish := &yamlStub{}
	h.config("version: 1\n")
	if present(h.repo, yamlish) {
		t.Error(".guardrails.yaml made the repo a YAML repo")
	}
	h.write("ci.yaml", "on: push\n")
	if !present(h.repo, yamlish) {
		t.Error("a real YAML file was not detected")
	}
}

// yamlStub owns *.yaml files.
type yamlStub struct{ stub }

func (*yamlStub) Match(p string) bool { return strings.HasSuffix(p, ".yaml") }
