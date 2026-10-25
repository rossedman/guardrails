package hook

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

const (
	editEvent  = `{"session_id":"s1","transcript_path":"/home/u/.claude/projects/p/s1.jsonl","cwd":"/repo","hook_event_name":"PreToolUse","permission_mode":"default","tool_name":"Edit","tool_input":{"file_path":"/repo/internal/a.go","old_string":"func a() {}","new_string":"func a() int { return 1 }","replace_all":false},"tool_use_id":"toolu_01"}`
	multiEvent = `{"session_id":"s1","cwd":"/repo","hook_event_name":"PreToolUse","tool_name":"MultiEdit","tool_input":{"file_path":"/repo/a.go","edits":[{"old_string":"a","new_string":"b"},{"old_string":"c","new_string":"d"}]}}`
	bashEvent  = `{"session_id":"s1","cwd":"/repo","hook_event_name":"PreToolUse","agent_id":"a1","agent_type":"test-writer","tool_name":"Bash","tool_input":{"command":"go test ./... && git push origin feat"},"unknown_future_field":{"x":[1,2]}}`
	promptEv   = `{"session_id":"s1","cwd":"/repo","hook_event_name":"UserPromptSubmit","prompt":"No, use the existing helper."}`
	stopEvent  = `{"session_id":"s1","cwd":"/repo","hook_event_name":"Stop","stop_hook_active":true}`
)

func BenchmarkReadInput(b *testing.B) {
	data := []byte(multiEvent)
	b.ReportAllocs()
	for b.Loop() {
		in, err := ReadInput(bytes.NewReader(data))
		if err != nil {
			b.Fatal(err)
		}
		tool, err := in.Tool()
		if err != nil {
			b.Fatal(err)
		}
		if len(tool.Replacements()) != 2 {
			b.Fatal("want 2 replacements")
		}
	}
}

func BenchmarkReadInputWrite(b *testing.B) {
	// A large Write payload: the common worst case for decoding.
	content := strings.Repeat("package x\n\nfunc f() { println(\"hi\") }\n", 2000)
	raw, err := json.Marshal(map[string]any{
		"session_id": "s1", "cwd": "/repo", "hook_event_name": "PreToolUse", "tool_name": "Write",
		"tool_input": map[string]any{"file_path": "/repo/big.go", "content": content},
	})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		in, err := ReadInput(bytes.NewReader(raw))
		if err != nil {
			b.Fatal(err)
		}
		tool, err := in.Tool()
		if err != nil || tool.Content != content {
			b.Fatal("bad tool input", err)
		}
	}
}

func FuzzReadInput(f *testing.F) {
	for _, s := range []string{editEvent, multiEvent, bashEvent, promptEv, stopEvent, "", "{}", "null", "[1]", "not json", `{"tool_input":[1]}`, `{"tool_input":null}`} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		in, err := ReadInput(bytes.NewReader(data))
		if err != nil {
			return
		}
		again, err := ReadInput(bytes.NewReader(data))
		if err != nil || !reflect.DeepEqual(in, again) {
			t.Fatalf("ReadInput not deterministic: %+v vs %+v (%v)", in, again, err)
		}

		// A decoded Input survives a marshal/decode round trip.
		enc, err := json.Marshal(in)
		if err != nil {
			t.Fatalf("marshal decoded input: %v", err)
		}
		back, err := ReadInput(bytes.NewReader(enc))
		if err != nil {
			t.Fatalf("decode re-encoded input %s: %v", enc, err)
		}
		enc2, err := json.Marshal(back)
		if err != nil || !bytes.Equal(enc, enc2) {
			t.Fatalf("round trip not stable: %s vs %s (%v)", enc, enc2, err)
		}

		tool, err := in.Tool()
		if err != nil {
			return
		}
		tool2, err := in.Tool()
		if err != nil || !reflect.DeepEqual(tool, tool2) {
			t.Fatalf("Tool not deterministic")
		}
		if len(in.ToolInput) == 0 && !reflect.DeepEqual(tool, ToolInput{}) {
			t.Fatalf("empty tool_input decoded to %+v", tool)
		}

		reps := tool.Replacements()
		switch {
		case len(tool.Edits) > 0:
			if !reflect.DeepEqual(reps, tool.Edits) {
				t.Fatalf("Replacements = %v, want Edits %v", reps, tool.Edits)
			}
		case tool.OldString != "" || tool.NewString != "":
			if len(reps) != 1 || reps[0].OldString != tool.OldString || reps[0].NewString != tool.NewString {
				t.Fatalf("Replacements = %v for single edit %+v", reps, tool)
			}
		default:
			if reps != nil {
				t.Fatalf("Replacements = %v, want nil", reps)
			}
		}
	})
}
