// Package hook defines the Claude Code hook wire format: the event JSON read
// from stdin and the decision JSON written to stdout.
package hook

import (
	"encoding/json"
	"fmt"
	"io"
)

// Event names, as passed to `guardrails hook <event>`.
const (
	SessionStart = "SessionStart"
	PreToolUse   = "PreToolUse"
	PostToolUse  = "PostToolUse"
	Stop         = "Stop"
	SubagentStop = "SubagentStop"
	SessionEnd   = "SessionEnd"
)

// Input is the JSON Claude Code writes to a hook's stdin. Fields that only
// apply to some events are left zero for the others.
type Input struct {
	SessionID      string          `json:"session_id"`
	TranscriptPath string          `json:"transcript_path,omitempty"`
	Cwd            string          `json:"cwd"`
	HookEventName  string          `json:"hook_event_name"`
	PermissionMode string          `json:"permission_mode,omitempty"`
	ScratchpadDir  string          `json:"scratchpad_dir,omitempty"`
	AgentID        string          `json:"agent_id,omitempty"`
	AgentType      string          `json:"agent_type,omitempty"`
	Source         string          `json:"source,omitempty"`
	Reason         string          `json:"reason,omitempty"`
	ToolName       string          `json:"tool_name,omitempty"`
	ToolInput      json.RawMessage `json:"tool_input,omitempty"`
	ToolUseID      string          `json:"tool_use_id,omitempty"`
	StopHookActive bool            `json:"stop_hook_active,omitempty"`
}

// ReadInput decodes an event from r.
func ReadInput(r io.Reader) (Input, error) {
	var in Input
	if err := json.NewDecoder(r).Decode(&in); err != nil {
		return in, fmt.Errorf("decode hook input: %w", err)
	}
	return in, nil
}

// ToolInput is the union of the tool inputs Guardrails inspects.
type ToolInput struct {
	FilePath   string `json:"file_path,omitempty"`
	Content    string `json:"content,omitempty"`
	OldString  string `json:"old_string,omitempty"`
	NewString  string `json:"new_string,omitempty"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
	Edits      []Edit `json:"edits,omitempty"`
	Command    string `json:"command,omitempty"`
}

// Edit is one replacement in a MultiEdit call.
type Edit struct {
	OldString string `json:"old_string"`
	NewString string `json:"new_string"`
}

// Tool decodes the tool_input field.
func (in Input) Tool() (ToolInput, error) {
	var t ToolInput
	if len(in.ToolInput) == 0 {
		return t, nil
	}
	if err := json.Unmarshal(in.ToolInput, &t); err != nil {
		return t, fmt.Errorf("decode tool_input: %w", err)
	}
	return t, nil
}

// Replacements returns the (old, new) pairs an Edit or MultiEdit makes.
func (t ToolInput) Replacements() []Edit {
	if len(t.Edits) > 0 {
		return t.Edits
	}
	if t.OldString != "" || t.NewString != "" {
		return []Edit{{OldString: t.OldString, NewString: t.NewString}}
	}
	return nil
}
