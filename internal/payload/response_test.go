package payload

import "testing"

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
