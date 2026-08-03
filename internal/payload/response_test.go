package payload

import (
	"encoding/json"
	"testing"
)

// TestDefer pins the defer decision constructor: it must target PreToolUse
// and carry the schema's "defer" value so a future rule can hand the verdict
// back to the default permission flow.
func TestDefer(t *testing.T) {
	r := Defer("leave it to the default flow")
	if r == nil || r.HookSpecificOutput == nil {
		t.Fatal("Defer must produce hookSpecificOutput")
	}
	if r.HookSpecificOutput.HookEventName != EventPreToolUse {
		t.Errorf("hookEventName = %q, want PreToolUse", r.HookSpecificOutput.HookEventName)
	}
	if r.HookSpecificOutput.PermissionDecision != DecisionDefer {
		t.Errorf("permissionDecision = %q, want %q", r.HookSpecificOutput.PermissionDecision, DecisionDefer)
	}
	if r.HookSpecificOutput.PermissionDecisionReason == "" {
		t.Error("a defer with no reason gives Claude nothing to act on")
	}
}

// TestReservedEvents: the reserved constants exist so future capabilities can
// reference them without stringly-typed event names.
func TestReservedEvents(t *testing.T) {
	for _, ev := range []string{EventPostToolUseFailure, EventStopFailure, EventWorktreeCreate} {
		if ev == "" {
			t.Error("a reserved event constant is empty")
		}
	}
}

// TestAllowWithRewriteDegradesOnUnmarshalableInput: a value that cannot be
// marshaled must not wedge the hook -- it degrades to a plain allow.
func TestAllowWithRewriteDegradesOnUnmarshalableInput(t *testing.T) {
	r := AllowWithRewrite("rewrite", make(chan int)) // channels cannot marshal
	if r == nil || r.HookSpecificOutput == nil {
		t.Fatal("AllowWithRewrite must still produce hookSpecificOutput")
	}
	if r.HookSpecificOutput.PermissionDecision != DecisionAllow {
		t.Errorf("decision = %q, want allow (degraded)", r.HookSpecificOutput.PermissionDecision)
	}
	if len(r.HookSpecificOutput.UpdatedInput) != 0 {
		t.Error("unmarshalable input must yield no updatedInput, not a corrupt one")
	}
}

// TestAllowWithRewriteCarriesUpdatedInput pins the happy path: a marshaled
// tool_input arrives in hookSpecificOutput.updatedInput.
func TestAllowWithRewriteCarriesUpdatedInput(t *testing.T) {
	r := AllowWithRewrite("use the venv", map[string]any{"command": "/venv/bin/pytest -x"})
	if r == nil || r.HookSpecificOutput == nil {
		t.Fatal("expected hookSpecificOutput")
	}
	if len(r.HookSpecificOutput.UpdatedInput) == 0 {
		t.Fatal("updatedInput must be present")
	}
	var got map[string]any
	if err := json.Unmarshal(r.HookSpecificOutput.UpdatedInput, &got); err != nil {
		t.Fatalf("updatedInput is not valid JSON: %v", err)
	}
	if got["command"] != "/venv/bin/pytest -x" {
		t.Errorf("updatedInput command = %v", got["command"])
	}
}
