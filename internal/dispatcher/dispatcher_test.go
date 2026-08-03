package dispatcher

import (
	"context"
	"testing"

	"github.com/zealot00/claude-toolkit/internal/payload"
)

func handler(resp *payload.Response) Handler {
	return func(context.Context, *payload.Event) (*payload.Response, error) {
		return resp, nil
	}
}

func TestDispatchToolMatching(t *testing.T) {
	d := New()
	var bashRan, allRan bool
	d.Register(&Route{Name: "bash-only", Event: payload.EventPreToolUse, Tools: []string{"Bash"},
		Handler: func(context.Context, *payload.Event) (*payload.Response, error) {
			bashRan = true
			return nil, nil
		}})
	d.Register(&Route{Name: "all-tools", Event: payload.EventPreToolUse,
		Handler: func(context.Context, *payload.Event) (*payload.Response, error) {
			allRan = true
			return nil, nil
		}})

	e := &payload.Event{HookEventName: payload.EventPreToolUse, ToolName: "Write"}
	if _, err := d.Dispatch(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if bashRan {
		t.Error("a Bash-only route ran for a Write event")
	}
	if !allRan {
		t.Error("a route with no tool filter did not run")
	}
}

func TestDispatchRegexMatcher(t *testing.T) {
	d := New()
	var ran bool
	d.Register(&Route{Name: "mcp", Event: payload.EventPreToolUse, Tools: []string{`mcp__.*`},
		Handler: func(context.Context, *payload.Event) (*payload.Response, error) {
			ran = true
			return nil, nil
		}})
	e := &payload.Event{HookEventName: payload.EventPreToolUse, ToolName: "mcp__github__create_issue"}
	if _, err := d.Dispatch(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Error("regex tool matcher did not match")
	}
}

// TestMergePrefersDeny pins the safety-first precedence: when two handlers
// disagree, the more restrictive verdict must win regardless of order.
func TestMergePrefersDeny(t *testing.T) {
	for _, order := range [][]*payload.Response{
		{payload.Allow("fine"), payload.Deny("dangerous")},
		{payload.Deny("dangerous"), payload.Allow("fine")},
	} {
		d := New()
		for i, r := range order {
			d.Register(&Route{Name: string(rune('a' + i)), Event: payload.EventPreToolUse, Handler: handler(r)})
		}
		got, err := d.Dispatch(context.Background(), &payload.Event{HookEventName: payload.EventPreToolUse})
		if err != nil {
			t.Fatal(err)
		}
		if got.HookSpecificOutput.PermissionDecision != payload.DecisionDeny {
			t.Errorf("got %q, want deny", got.HookSpecificOutput.PermissionDecision)
		}
		if got.HookSpecificOutput.PermissionDecisionReason != "dangerous" {
			t.Errorf("reason did not follow the decision: %q", got.HookSpecificOutput.PermissionDecisionReason)
		}
	}
}

func TestMergeAccumulatesContext(t *testing.T) {
	d := New()
	d.Register(&Route{Name: "a", Event: payload.EventSessionStart,
		Handler: handler(payload.Context(payload.EventSessionStart, "first"))})
	d.Register(&Route{Name: "b", Event: payload.EventSessionStart,
		Handler: handler(payload.Context(payload.EventSessionStart, "second"))})

	got, err := d.Dispatch(context.Background(), &payload.Event{HookEventName: payload.EventSessionStart})
	if err != nil {
		t.Fatal(err)
	}
	want := "first\n\nsecond"
	if got.HookSpecificOutput.AdditionalContext != want {
		t.Errorf("got %q, want %q", got.HookSpecificOutput.AdditionalContext, want)
	}
}

// TestBrokenHandlerDoesNotSuppressVerdict is the reason Dispatch collects
// errors instead of returning early: one broken hook must not be able to
// swallow another hook's deny.
func TestBrokenHandlerDoesNotSuppressVerdict(t *testing.T) {
	d := New()
	d.Register(&Route{Name: "broken", Event: payload.EventPreToolUse,
		Handler: func(context.Context, *payload.Event) (*payload.Response, error) {
			return nil, context.DeadlineExceeded
		}})
	d.Register(&Route{Name: "guard", Event: payload.EventPreToolUse,
		Handler: handler(payload.Deny("dangerous"))})

	got, err := d.Dispatch(context.Background(), &payload.Event{HookEventName: payload.EventPreToolUse})
	if err == nil {
		t.Error("the handler error should still be reported")
	}
	if got == nil || got.HookSpecificOutput.PermissionDecision != payload.DecisionDeny {
		t.Fatalf("the deny was lost: %+v", got)
	}
}

func TestMatcherDerivation(t *testing.T) {
	d := New()
	d.Register(&Route{Name: "a", Event: payload.EventPreToolUse, Tools: []string{"Bash", "Write"}, Handler: handler(nil)})
	d.Register(&Route{Name: "b", Event: payload.EventPreToolUse, Tools: []string{"Write", "Edit"}, Handler: handler(nil)})
	if got, want := d.Matcher(payload.EventPreToolUse), "Bash|Write|Edit"; got != want {
		t.Errorf("got %q, want %q (union, deduplicated, order preserved)", got, want)
	}

	d.Register(&Route{Name: "c", Event: payload.EventSessionStart, Handler: handler(nil)})
	if got := d.Matcher(payload.EventSessionStart); got != "*" {
		t.Errorf("got %q, want * for a route with no tool filter", got)
	}
}

func TestDispatchUnknownEventIsNoOp(t *testing.T) {
	d := New()
	d.Register(&Route{Name: "a", Event: payload.EventPreToolUse, Handler: handler(payload.Deny("no"))})
	got, err := d.Dispatch(context.Background(), &payload.Event{HookEventName: "SomeFutureEvent"})
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("want no response for an unregistered event, got %+v", got)
	}
}
