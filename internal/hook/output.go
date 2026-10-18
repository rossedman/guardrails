package hook

import (
	"encoding/json"
	"io"
)

// Permission decisions for PreToolUse.
const (
	Allow = "allow"
	Deny  = "deny"
	Ask   = "ask"
)

// Output is the JSON a hook writes to stdout. Every blocking path in
// Guardrails uses a JSON decision with exit code 0; exit 1 would be a
// non-blocking error in Claude Code.
type Output struct {
	Continue      *bool        `json:"continue,omitempty"`
	StopReason    string       `json:"stopReason,omitempty"`
	SystemMessage string       `json:"systemMessage,omitempty"`
	Decision      string       `json:"decision,omitempty"`
	Reason        string       `json:"reason,omitempty"`
	Specific      *SpecificOut `json:"hookSpecificOutput,omitempty"`
}

// SpecificOut is the hookSpecificOutput object.
type SpecificOut struct {
	HookEventName            string `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
	AdditionalContext        string `json:"additionalContext,omitempty"`
}

// Block returns an output that blocks with reason (PostToolUse, Stop,
// SubagentStop).
func Block(reason string) Output {
	return Output{Decision: "block", Reason: reason}
}

// Permission returns a PreToolUse permission decision.
func Permission(decision, reason string) Output {
	return Output{Specific: &SpecificOut{
		HookEventName:            PreToolUse,
		PermissionDecision:       decision,
		PermissionDecisionReason: reason,
	}}
}

// Context returns an output that injects additional context for event.
func Context(event, text string) Output {
	if text == "" {
		return Output{}
	}
	return Output{Specific: &SpecificOut{HookEventName: event, AdditionalContext: text}}
}

// Blocking reports whether the output stops the action.
func (o Output) Blocking() bool {
	if o.Decision == "block" {
		return true
	}
	return o.Specific != nil && o.Specific.PermissionDecision == Deny
}

// IsZero reports whether the output carries nothing.
func (o Output) IsZero() bool {
	return o.Continue == nil && o.StopReason == "" && o.SystemMessage == "" &&
		o.Decision == "" && o.Reason == "" && o.Specific == nil
}

// Write encodes o to w. An empty output writes nothing, which Claude Code
// treats as success.
func (o Output) Write(w io.Writer) error {
	if o.IsZero() {
		return nil
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(o)
}
