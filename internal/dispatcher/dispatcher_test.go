package dispatcher

import (
	"context"
	"os"
	"testing"
	"time"

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
	d.Register(&Route{Name: "bash-only", Events: []string{payload.EventPreToolUse}, Tools: []string{"Bash"},
		Handler: func(context.Context, *payload.Event) (*payload.Response, error) {
			bashRan = true
			return nil, nil
		}})
	d.Register(&Route{Name: "all-tools", Events: []string{payload.EventPreToolUse},
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
	d.Register(&Route{Name: "mcp", Events: []string{payload.EventPreToolUse}, Tools: []string{`mcp__.*`},
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
			d.Register(&Route{Name: string(rune('a' + i)), Events: []string{payload.EventPreToolUse}, Handler: handler(r)})
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
	d.Register(&Route{Name: "a", Events: []string{payload.EventSessionStart},
		Handler: handler(payload.Context(payload.EventSessionStart, "first"))})
	d.Register(&Route{Name: "b", Events: []string{payload.EventSessionStart},
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
	d.Register(&Route{Name: "broken", Events: []string{payload.EventPreToolUse},
		Handler: func(context.Context, *payload.Event) (*payload.Response, error) {
			return nil, context.DeadlineExceeded
		}})
	d.Register(&Route{Name: "guard", Events: []string{payload.EventPreToolUse},
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
	d.Register(&Route{Name: "a", Events: []string{payload.EventPreToolUse}, Tools: []string{"Bash", "Write"}, Handler: handler(nil)})
	d.Register(&Route{Name: "b", Events: []string{payload.EventPreToolUse}, Tools: []string{"Write", "Edit"}, Handler: handler(nil)})
	if got, want := d.Matcher(payload.EventPreToolUse), "^(Bash|Write|Edit)$"; got != want {
		t.Errorf("got %q, want %q (union, deduplicated, anchored)", got, want)
	}

	d.Register(&Route{Name: "c", Events: []string{payload.EventSessionStart}, Handler: handler(nil)})
	if got := d.Matcher(payload.EventSessionStart); got != "*" {
		t.Errorf("got %q, want * for a route with no tool filter", got)
	}
}

func TestDispatchUnknownEventIsNoOp(t *testing.T) {
	d := New()
	d.Register(&Route{Name: "a", Events: []string{payload.EventPreToolUse}, Handler: handler(payload.Deny("no"))})
	got, err := d.Dispatch(context.Background(), &payload.Event{HookEventName: "SomeFutureEvent"})
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("want no response for an unregistered event, got %+v", got)
	}
}

// TestDispatchCapabilityFilter pins --cap: dispatch must narrow to one
// capability, and the merge still applies within that subset.
func TestDispatchCapabilityFilter(t *testing.T) {
	d := New()
	var guardRan, formatRan bool
	d.Register(&Route{Name: "guard", Events: []string{payload.EventPreToolUse},
		Handler: func(context.Context, *payload.Event) (*payload.Response, error) {
			guardRan = true
			return payload.Deny("dangerous"), nil
		}})
	d.Register(&Route{Name: "format", Events: []string{payload.EventPreToolUse},
		Handler: func(context.Context, *payload.Event) (*payload.Response, error) {
			formatRan = true
			return nil, nil
		}})

	e := &payload.Event{HookEventName: payload.EventPreToolUse}

	got, err := d.Dispatch(context.Background(), e, "guard")
	if err != nil {
		t.Fatal(err)
	}
	if !guardRan {
		t.Error("guard did not run under --cap=guard")
	}
	if formatRan {
		t.Error("format ran under --cap=guard; capability filter is broken")
	}
	if got == nil || got.HookSpecificOutput.PermissionDecision != payload.DecisionDeny {
		t.Errorf("guard's deny was lost: %+v", got)
	}

	// Unknown capability name: no route matches, no opinion, no error.
	guardRan, formatRan = false, false
	got, err = d.Dispatch(context.Background(), e, "no-such-cap")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil || guardRan || formatRan {
		t.Errorf("unknown capability should be a silent no-op, got resp=%+v guard=%v format=%v", got, guardRan, formatRan)
	}
}

// TestCwdEnvFallback pins the cwd override in Dispatch: an event whose
// payload carries no cwd must pick up CLAUDE_PROJECT_DIR, so SessionEnd and
// friends still know where the project is.
func TestCwdEnvFallback(t *testing.T) {
	old, had := os.LookupEnv("CLAUDE_PROJECT_DIR")
	os.Setenv("CLAUDE_PROJECT_DIR", "/some/project")
	defer func() {
		if had {
			os.Setenv("CLAUDE_PROJECT_DIR", old)
		} else {
			os.Unsetenv("CLAUDE_PROJECT_DIR")
		}
	}()

	d := New()
	var gotCwd string
	d.Register(&Route{Name: "probe", Events: []string{payload.EventSessionStart},
		Handler: func(_ context.Context, e *payload.Event) (*payload.Response, error) {
			gotCwd = e.Cwd
			return nil, nil
		}})

	e := &payload.Event{HookEventName: payload.EventSessionStart} // cwd empty
	if _, err := d.Dispatch(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if gotCwd != "/some/project" {
		t.Errorf("handler saw cwd %q, want CLAUDE_PROJECT_DIR fallback", gotCwd)
	}
}

// TestRemaining pins the timeout-awareness helper handlers use to degrade.
func TestRemaining(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	left, ok := Remaining(ctx)
	if !ok || left <= 0 || left > time.Second {
		t.Errorf("Remaining = %v, %v; want positive and bounded by the timeout", left, ok)
	}
	if _, ok := Remaining(context.Background()); ok {
		t.Error("Remaining on a context without deadline should report no deadline")
	}
}
