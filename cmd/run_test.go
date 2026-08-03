package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestRunEndToEnd drives the exact path Claude Code uses: JSON in on stdin,
// JSON out on stdout.
func TestRunEndToEnd(t *testing.T) {
	tests := []struct {
		name       string
		stdin      string
		wantOutput bool
		wantDeny   bool
	}{
		{
			name:       "denies a destructive command",
			stdin:      `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"rm -rf /"}}`,
			wantOutput: true,
			wantDeny:   true,
		},
		{
			name:       "stays silent on an ordinary command",
			stdin:      `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"go test ./..."}}`,
			wantOutput: false,
		},
		{
			name:       "ignores an event it does not handle",
			stdin:      `{"hook_event_name":"Notification","message":"hello"}`,
			wantOutput: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			if code := run(strings.NewReader(tt.stdin), &out, "pre", 5*time.Second); code != 0 {
				t.Fatalf("exit code %d; run must always exit 0", code)
			}
			if !tt.wantOutput {
				if out.Len() != 0 {
					t.Fatalf("want no output, got %s", out.String())
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
			if code := run(strings.NewReader(in), &out, "", 5*time.Second); code != 0 {
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
	if code := run(strings.NewReader(stdin), &out, "post", 5*time.Second); code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if !strings.Contains(out.String(), `"deny"`) {
		t.Errorf("the guard should still have denied; got %s", out.String())
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
	for _, s := range specs {
		if s.Matcher == "" {
			t.Errorf("%s has an empty matcher", s.Event)
		}
		if !strings.HasPrefix(s.Command, "claude-toolkit run --event=") {
			t.Errorf("%s command is malformed: %q", s.Event, s.Command)
		}
		if s.Timeout <= 0 {
			t.Errorf("%s has no timeout; a hung hook would stall the session", s.Event)
		}
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
