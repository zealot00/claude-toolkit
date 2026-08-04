package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestRunEndToEnd drives the exact path Claude Code uses: JSON in on stdin,
// JSON out on stdout.
func TestRunEndToEnd(t *testing.T) {
	tests := []struct {
		name     string
		stdin    string
		wantJSON bool // output must be valid JSON (always true today)
		wantDeny bool // output must carry a deny verdict
		wantNoOp bool // output must be the "no opinion" empty object
	}{
		{
			name:     "denies a destructive command",
			stdin:    `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"rm -rf /"}}`,
			wantJSON: true,
			wantDeny: true,
		},
		{
			name:     "stays silent on an ordinary command",
			stdin:    `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"go test ./..."}}`,
			wantJSON: true,
			wantNoOp: true,
		},
		{
			name:     "ignores an event it does not handle",
			stdin:    `{"hook_event_name":"Notification","message":"hello"}`,
			wantJSON: true,
			wantNoOp: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			if code := run(strings.NewReader(tt.stdin), &out, "pre", "", 5*time.Second); code != 0 {
				t.Fatalf("exit code %d; run must always exit 0", code)
			}
			if !tt.wantJSON {
				t.Fatal("every run must emit valid JSON today")
			}
			if !json.Valid(out.Bytes()) {
				t.Fatalf("output is not valid JSON: %s", out.String())
			}
			if tt.wantNoOp {
				if strings.TrimSpace(out.String()) != "{}" {
					t.Fatalf("want the empty no-op object, got %s", out.String())
				}
				return
			}
			var resp struct {
				HookSpecificOutput struct {
					HookEventName            string `json:"hookEventName"`
					PermissionDecision       string `json:"permissionDecision"`
					PermissionDecisionReason string `json:"permissionDecisionReason"`
				} `json:"hookSpecificOutput"`
			}
			if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
				t.Fatalf("output is not valid JSON: %v\n%s", err, out.String())
			}
			if resp.HookSpecificOutput.HookEventName != "PreToolUse" {
				t.Errorf("hookEventName = %q, want PreToolUse", resp.HookSpecificOutput.HookEventName)
			}
			if tt.wantDeny && resp.HookSpecificOutput.PermissionDecision != "deny" {
				t.Errorf("permissionDecision = %q, want deny", resp.HookSpecificOutput.PermissionDecision)
			}
			if resp.HookSpecificOutput.PermissionDecisionReason == "" {
				t.Error("a deny with no reason gives Claude nothing to act on")
			}
		})
	}
}

// TestRunFailsOpen covers the invariant that matters most in production: no
// input, however malformed, may produce a non-zero exit or garbage on stdout.
// Exit 2 would block the user's tool call; other non-zero codes surface an
// error in their transcript.
func TestRunFailsOpen(t *testing.T) {
	inputs := []string{
		"",
		"not json at all",
		"{",
		`{"hook_event_name":""}`,
		`null`,
		`[]`,
		`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":"a string, not an object"}`,
		`{"hook_event_name":"PreToolUse","tool_name":"Bash"}`,
		`{"hook_event_name":"PostToolUse","tool_name":"Write","tool_input":{"file_path":"/nonexistent/nope.go"}}`,
	}
	for _, in := range inputs {
		t.Run(truncateName(in), func(t *testing.T) {
			var out bytes.Buffer
			if code := run(strings.NewReader(in), &out, "", "", 5*time.Second); code != 0 {
				t.Errorf("exit code %d for input %q; must be 0", code, in)
			}
			if out.Len() > 0 && !json.Valid(out.Bytes()) {
				t.Errorf("emitted invalid JSON for input %q: %s", in, out.String())
			}
		})
	}
}

// TestRunTrustsPayloadOverFlag documents that --event is presentational: a
// miswired matcher must not change what the hook decides.
func TestRunTrustsPayloadOverFlag(t *testing.T) {
	const stdin = `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"rm -rf /"}}`
	var out bytes.Buffer
	// Deliberately wrong flag for this payload.
	if code := run(strings.NewReader(stdin), &out, "post", "", 5*time.Second); code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if !strings.Contains(out.String(), `"deny"`) {
		t.Errorf("the guard should still have denied; got %s", out.String())
	}
}

// TestRunCapFilter pins --cap: dispatch must narrow to one capability, and a
// wrong --cap must degrade to a silent no-op rather than an error.
func TestRunCapFilter(t *testing.T) {
	const stdin = `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"rm -rf /"}}`

	var out bytes.Buffer
	if code := run(strings.NewReader(stdin), &out, "pre", "guard", 5*time.Second); code != 0 {
		t.Fatalf("exit code %d; run must always exit 0", code)
	}
	if !strings.Contains(out.String(), `"deny"`) {
		t.Errorf("guard should have denied under --cap=guard; got %s", out.String())
	}

	out.Reset()
	if code := run(strings.NewReader(stdin), &out, "pre", "format", 5*time.Second); code != 0 {
		t.Fatalf("exit code %d for a capability that does not handle the event", code)
	}
	if strings.TrimSpace(out.String()) != "{}" {
		t.Errorf("format does not listen on PreToolUse; expected the no-op object, got %s", out.String())
	}

	out.Reset()
	if code := run(strings.NewReader(stdin), &out, "pre", "no-such-cap", 5*time.Second); code != 0 {
		t.Fatalf("exit code %d for an unknown capability; must fail open", code)
	}
	if strings.TrimSpace(out.String()) != "{}" {
		t.Errorf("unknown capability should produce the no-op object; got %s", out.String())
	}
}

func TestCanonicalEvent(t *testing.T) {
	cases := map[string]string{
		"pre":        "PreToolUse",
		"PreToolUse": "PreToolUse",
		"post":       "PostToolUse",
		"session":    "SessionStart",
		"prompt":     "UserPromptSubmit",
	}
	for in, want := range cases {
		got, ok := canonicalEvent(in)
		if !ok || got != want {
			t.Errorf("canonicalEvent(%q) = %q, %v; want %q, true", in, got, ok, want)
		}
	}
	if _, ok := canonicalEvent("nonsense"); ok {
		t.Error("canonicalEvent accepted an unknown name")
	}
}

// TestBuildSpecsMatchesRoutes checks the config generator stays in sync with
// the registered hooks, which is what lets doctor detect stale settings.
func TestBuildSpecsMatchesRoutes(t *testing.T) {
	specs := buildSpecs("claude-toolkit")
	if len(specs) == 0 {
		t.Fatal("no specs generated")
	}
	seen := map[string]string{} // capability -> events covered
	for _, s := range specs {
		if s.Matcher == "" {
			t.Errorf("%s has an empty matcher", s.Event)
		}
		if s.Capability == "" {
			t.Errorf("%s spec carries no capability name", s.Event)
		}
		if prev, dup := seen[s.Capability]; dup && prev == s.Event {
			t.Errorf("capability %q generated two specs for the same event %s", s.Capability, s.Event)
		}
		seen[s.Capability] = s.Event
		want := fmt.Sprintf("claude-toolkit run --event=%s --cap=%s", eventAlias[s.Event], s.Capability)
		if s.Command != want {
			t.Errorf("%s command is malformed: %q, want %q", s.Capability, s.Command, want)
		}
		if s.Timeout <= 0 {
			t.Errorf("%s has no timeout; a hung hook would stall the session", s.Event)
		}
	}

	// Every registered capability appears, and multi-event capabilities get a
	// group per event (this is what the review flagged as a blocking bug).
	wantEvents := map[string][]string{
		"guard":     {"PreToolUse"},
		"format":    {"PostToolUse"},
		"heal":      {"PostToolUse"},
		"enrich":    {"SessionStart", "UserPromptSubmit"},
		"loopguard": {"PreToolUse", "PostToolUse", "PostToolUseFailure"},
		"notify":    {"PreToolUse", "PostToolUse", "PostToolUseFailure"},
		"envfix":    {"PreToolUse"},
	}
	covered := map[string][]string{}
	for _, s := range specs {
		covered[s.Capability] = append(covered[s.Capability], s.Event)
	}
	if len(covered) != len(wantEvents) {
		t.Fatalf("capabilities generated = %v, want %v", covered, wantEvents)
	}
	for cap, want := range wantEvents {
		got := covered[cap]
		if len(got) != len(want) {
			t.Errorf("%s events = %v, want %v (multi-event registration missing)", cap, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%s events = %v, want %v", cap, got, want)
			}
		}
	}

	// A restricted build must be a strict subset of the full build.
	only := buildSpecs("claude-toolkit", "guard")
	if len(only) != 1 || only[0].Capability != "guard" {
		t.Fatalf("buildSpecs(guard) = %+v, want exactly the guard spec", only)
	}
}

func TestShellQuote(t *testing.T) {
	if got := shellQuote("/usr/local/bin/claude-toolkit"); got != "/usr/local/bin/claude-toolkit" {
		t.Errorf("unnecessary quoting: %q", got)
	}
	if got := shellQuote("/Users/my name/bin/claude-toolkit"); got != `"/Users/my name/bin/claude-toolkit"` {
		t.Errorf("path with a space was not quoted: %q", got)
	}
}

func truncateName(s string) string {
	if s == "" {
		return "empty"
	}
	if len(s) > 30 {
		return s[:30]
	}
	return s
}
