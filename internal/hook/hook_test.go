package hook

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestReadInput(t *testing.T) {
	t.Parallel()
	in, err := ReadInput(strings.NewReader(`{
		"session_id": "s1", "cwd": "/repo", "hook_event_name": "PreToolUse",
		"agent_id": "a1", "agent_type": "test-writer", "stop_hook_active": true,
		"tool_name": "Edit", "tool_input": {"file_path": "/repo/a.go", "old_string": "x", "new_string": "y", "replace_all": true},
		"unknown_future_field": 1
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if in.SessionID != "s1" || in.Cwd != "/repo" || in.HookEventName != PreToolUse || in.AgentType != "test-writer" || !in.StopHookActive {
		t.Errorf("Input = %+v", in)
	}
	tool, err := in.Tool()
	if err != nil {
		t.Fatal(err)
	}
	want := ToolInput{FilePath: "/repo/a.go", OldString: "x", NewString: "y", ReplaceAll: true}
	if !reflect.DeepEqual(tool, want) {
		t.Errorf("Tool = %+v", tool)
	}
}

func TestReadInputError(t *testing.T) {
	t.Parallel()
	if _, err := ReadInput(strings.NewReader("not json")); err == nil || !strings.Contains(err.Error(), "decode hook input") {
		t.Errorf("err = %v", err)
	}
}

func TestTool(t *testing.T) {
	t.Parallel()
	empty, err := Input{}.Tool()
	if err != nil || !reflect.DeepEqual(empty, ToolInput{}) {
		t.Errorf("empty tool_input = %+v, %v", empty, err)
	}
	if _, err := (Input{ToolInput: json.RawMessage(`[1]`)}).Tool(); err == nil {
		t.Error("expected decode error")
	}
	bash, err := Input{ToolInput: json.RawMessage(`{"command":"ls"}`)}.Tool()
	if err != nil || bash.Command != "ls" {
		t.Errorf("bash = %+v, %v", bash, err)
	}
}

func TestReplacements(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   ToolInput
		want []Edit
	}{
		{"write", ToolInput{Content: "x"}, nil},
		{"edit", ToolInput{OldString: "a", NewString: "b"}, []Edit{{"a", "b"}}},
		{"insert only", ToolInput{NewString: "b"}, []Edit{{"", "b"}}},
		{"multi", ToolInput{Edits: []Edit{{"a", "b"}, {"c", "d"}}}, []Edit{{"a", "b"}, {"c", "d"}}},
	}
	for _, tc := range tests {
		if got := tc.in.Replacements(); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestOutputs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		out      Output
		want     string
		blocking bool
	}{
		{"empty", Output{}, "", false},
		{"block", Block("tests fail"), `{"decision":"block","reason":"tests fail"}`, true},
		{"deny", Permission(Deny, "no <way>"), `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"no <way>"}}`, true},
		{"ask", Permission(Ask, "sure?"), `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask","permissionDecisionReason":"sure?"}}`, false},
		{"context", Context(SessionStart, "hello"), `{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"hello"}}`, false},
		{"empty context", Context(SessionStart, ""), "", false},
		{"system message", Output{SystemMessage: "hi"}, `{"systemMessage":"hi"}`, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			if err := tc.out.Write(&buf); err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimSpace(buf.String()); got != tc.want {
				t.Errorf("Write = %s, want %s", got, tc.want)
			}
			if tc.out.Blocking() != tc.blocking {
				t.Errorf("Blocking = %v", tc.out.Blocking())
			}
			if tc.out.IsZero() != (tc.want == "") {
				t.Errorf("IsZero = %v", tc.out.IsZero())
			}
		})
	}
}

func TestIsZeroFields(t *testing.T) {
	t.Parallel()
	f := false
	for _, o := range []Output{{Continue: &f}, {StopReason: "x"}, {Decision: "block"}, {Reason: "x"}} {
		if o.IsZero() {
			t.Errorf("%+v reported zero", o)
		}
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

func TestWriteError(t *testing.T) {
	t.Parallel()
	if err := Block("x").Write(failWriter{}); err == nil {
		t.Error("expected write error")
	}
}
