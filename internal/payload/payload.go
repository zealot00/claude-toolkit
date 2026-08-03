// Package payload provides strongly-typed decoding of the JSON documents
// Claude Code writes to a hook's stdin, and typed constructors for the JSON
// documents a hook writes back to stdout.
//
// Schema reference: https://code.claude.com/docs/en/hooks
package payload

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Hook event names. Only the events this toolkit handles are enumerated;
// Event.HookEventName carries whatever Claude Code sent regardless. The last
// three are reserved: the dispatcher registers no route for them yet, but they
// exist so future capabilities can reference them without stringly-typed
// event names.
const (
	EventPreToolUse       = "PreToolUse"
	EventPostToolUse      = "PostToolUse"
	EventSessionStart     = "SessionStart"
	EventSessionEnd       = "SessionEnd"
	EventUserPromptSubmit = "UserPromptSubmit"
	EventStop             = "Stop"

	EventPostToolUseFailure = "PostToolUseFailure"
	EventStopFailure        = "StopFailure"
	EventWorktreeCreate     = "WorktreeCreate"
)

// Permission decisions valid in HookSpecificOutput.PermissionDecision.
const (
	DecisionAllow = "allow"
	DecisionDeny  = "deny"
	DecisionAsk   = "ask"
	// DecisionDefer leaves the verdict to Claude Code's normal permission
	// flow. It exists in the schema, but some Claude Code versions have bugs
	// around defer (dropped tool results, hangs under bypassPermissions);
	// prefer deny/ask unless the default flow is genuinely needed.
	DecisionDefer = "defer"
)

// Event is the union of fields Claude Code sends on stdin. Fields absent for a
// given event stay at their zero value. ToolInput and ToolResponse are held as
// raw JSON because their shape depends on ToolName; use the typed accessors.
type Event struct {
	// Common to every event.
	SessionID      string `json:"session_id"`
	PromptID       string `json:"prompt_id"`
	TranscriptPath string `json:"transcript_path"`
	Cwd            string `json:"cwd"`
	PermissionMode string `json:"permission_mode"`
	HookEventName  string `json:"hook_event_name"`

	// Subagent context, present when running inside a subagent.
	AgentID   string `json:"agent_id"`
	AgentType string `json:"agent_type"`

	// PreToolUse / PostToolUse / PostToolUseFailure.
	ToolName     string          `json:"tool_name"`
	ToolInput    json.RawMessage `json:"tool_input"`
	ToolResponse json.RawMessage `json:"tool_response"`
	ToolUseID    string          `json:"tool_use_id"`

	// SessionStart.
	Source string `json:"source"`

	// SessionEnd.
	Reason string `json:"reason"`

	// UserPromptSubmit.
	Prompt string `json:"prompt"`
}

// BashInput is the tool_input shape for the Bash tool.
type BashInput struct {
	Command         string `json:"command"`
	Description     string `json:"description"`
	Timeout         int    `json:"timeout"`
	RunInBackground bool   `json:"run_in_background"`
}

// FileInput is the tool_input shape shared by Read, Write, Edit and NotebookEdit.
// NotebookEdit uses notebook_path rather than file_path; Path() normalizes that.
type FileInput struct {
	FilePath     string `json:"file_path"`
	NotebookPath string `json:"notebook_path"`
	Content      string `json:"content"`
	OldString    string `json:"old_string"`
	NewString    string `json:"new_string"`
}

// MultiEditInput is the tool_input shape for MultiEdit: a batch of edits
// across possibly several files.
type MultiEditInput struct {
	Edits []FileEdit `json:"edits"`
}

// FileEdit is one edit inside a MultiEdit call.
type FileEdit struct {
	FilePath  string `json:"file_path"`
	OldString string `json:"old_string"`
	NewString string `json:"new_string"`
}

// Path returns whichever path field the tool populated.
func (f FileInput) Path() string {
	if f.FilePath != "" {
		return f.FilePath
	}
	return f.NotebookPath
}

// ErrWrongTool is returned by a typed accessor when the event carries a
// different tool than the accessor decodes.
var ErrWrongTool = errors.New("payload: tool_input belongs to a different tool")

// Decode reads a single JSON event from r.
func Decode(r io.Reader) (*Event, error) {
	var e Event
	dec := json.NewDecoder(r)
	if err := dec.Decode(&e); err != nil {
		return nil, fmt.Errorf("payload: decode stdin: %w", err)
	}
	if e.HookEventName == "" {
		return nil, errors.New("payload: missing hook_event_name")
	}
	return &e, nil
}

// Bash decodes tool_input as a Bash invocation. It returns ErrWrongTool unless
// ToolName is "Bash".
func (e *Event) Bash() (BashInput, error) {
	var in BashInput
	if e.ToolName != "Bash" {
		return in, ErrWrongTool
	}
	if len(e.ToolInput) == 0 {
		return in, nil
	}
	if err := json.Unmarshal(e.ToolInput, &in); err != nil {
		return in, fmt.Errorf("payload: decode Bash tool_input: %w", err)
	}
	return in, nil
}

// File decodes tool_input as a file operation. Unlike Bash it does not gate on
// ToolName, because Write, Edit, Read and NotebookEdit all share the shape.
func (e *Event) File() (FileInput, error) {
	var in FileInput
	if len(e.ToolInput) == 0 {
		return in, nil
	}
	if err := json.Unmarshal(e.ToolInput, &in); err != nil {
		return in, fmt.Errorf("payload: decode file tool_input: %w", err)
	}
	return in, nil
}
