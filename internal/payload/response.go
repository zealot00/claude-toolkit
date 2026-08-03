package payload

import (
	"encoding/json"
	"fmt"
	"io"
)

// Response is the JSON document a hook writes to stdout. Claude Code only
// parses it when the process exits 0. Every field is optional; an empty
// Response is a no-op.
type Response struct {
	// Universal.
	Continue       *bool  `json:"continue,omitempty"`
	StopReason     string `json:"stopReason,omitempty"`
	SuppressOutput bool   `json:"suppressOutput,omitempty"`
	SystemMessage  string `json:"systemMessage,omitempty"`

	// Top-level decision. Valid for PostToolUse, UserPromptSubmit, Stop and
	// friends -- NOT for PreToolUse, which uses HookSpecificOutput instead.
	Decision string `json:"decision,omitempty"`
	Reason   string `json:"reason,omitempty"`

	HookSpecificOutput *HookSpecificOutput `json:"hookSpecificOutput,omitempty"`
}

// HookSpecificOutput carries per-event fields. HookEventName is mandatory
// whenever this object is present.
type HookSpecificOutput struct {
	HookEventName string `json:"hookEventName"`

	// PreToolUse.
	PermissionDecision       string          `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string          `json:"permissionDecisionReason,omitempty"`
	UpdatedInput             json.RawMessage `json:"updatedInput,omitempty"`

	// SessionStart, PreToolUse, PostToolUse and other context-injecting events.
	AdditionalContext string `json:"additionalContext,omitempty"`
}

// Deny blocks a PreToolUse tool call. The reason is shown to Claude so it can
// choose a different approach.
func Deny(reason string) *Response {
	return &Response{HookSpecificOutput: &HookSpecificOutput{
		HookEventName:            EventPreToolUse,
		PermissionDecision:       DecisionDeny,
		PermissionDecisionReason: reason,
	}}
}

// Ask forces the permission prompt for a PreToolUse tool call even if a rule
// would otherwise have auto-approved it.
func Ask(reason string) *Response {
	return &Response{HookSpecificOutput: &HookSpecificOutput{
		HookEventName:            EventPreToolUse,
		PermissionDecision:       DecisionAsk,
		PermissionDecisionReason: reason,
	}}
}

// Allow approves a PreToolUse tool call, bypassing the permission prompt.
func Allow(reason string) *Response {
	return &Response{HookSpecificOutput: &HookSpecificOutput{
		HookEventName:            EventPreToolUse,
		PermissionDecision:       DecisionAllow,
		PermissionDecisionReason: reason,
	}}
}

// Context injects text into the conversation for the given event without
// making any allow/deny decision.
func Context(event, text string) *Response {
	return &Response{HookSpecificOutput: &HookSpecificOutput{
		HookEventName:     event,
		AdditionalContext: text,
	}}
}

// Block sets the top-level block decision, used by PostToolUse, Stop and
// UserPromptSubmit. It is not valid for PreToolUse -- use Deny there.
func Block(reason string) *Response {
	return &Response{Decision: "block", Reason: reason}
}

// Write emits r as JSON to w. A nil Response writes nothing, which Claude Code
// reads as "this hook has no opinion".
func (r *Response) Write(w io.Writer) error {
	if r == nil {
		return nil
	}
	enc := json.NewEncoder(w)
	if err := enc.Encode(r); err != nil {
		return fmt.Errorf("payload: encode response: %w", err)
	}
	return nil
}
