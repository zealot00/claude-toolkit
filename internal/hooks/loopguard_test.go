package hooks

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/zealot00/claude-toolkit/internal/payload"
)

// withIsolatedHome points CLAUDE_TOOLKIT_HOME at a temp dir so loop-guard
// state never touches the real ~/.claude-toolkit.
func withIsolatedHome(t *testing.T) {
	t.Helper()
	t.Setenv("CLAUDE_TOOLKIT_HOME", filepath.Join(t.TempDir(), "home"))
}

func bashEvent(event, cmd string, toolResponse json.RawMessage) *payload.Event {
	in, _ := json.Marshal(map[string]string{"command": cmd})
	return &payload.Event{
		HookEventName: event,
		ToolName:      "Bash",
		ToolInput:     in,
		ToolResponse:  toolResponse,
	}
}

func responseWithExit(code int) json.RawMessage {
	b, _ := json.Marshal(map[string]int{"exit_code": code})
	return b
}

func TestLoopGuardBlocksAfterThreeFailures(t *testing.T) {
	withIsolatedHome(t)
	const cmd = "go test ./flaky"

	// Two failures: no opinion.
	for i := 0; i < 2; i++ {
		resp, err := loopGuard(context.Background(), bashEvent(payload.EventPostToolUse, cmd, responseWithExit(1)))
		if err != nil {
			t.Fatal(err)
		}
		if resp != nil {
			t.Fatalf("recording failure %d should not produce a response, got %+v", i+1, resp)
		}
	}
	pre, err := loopGuard(context.Background(), bashEvent(payload.EventPreToolUse, cmd, nil))
	if err != nil {
		t.Fatal(err)
	}
	if pre != nil {
		t.Fatalf("2 failures must not block yet, got %+v", pre)
	}

	// Third failure: the next PreToolUse run is denied.
	if _, err := loopGuard(context.Background(), bashEvent(payload.EventPostToolUse, cmd, responseWithExit(1))); err != nil {
		t.Fatal(err)
	}
	pre, err = loopGuard(context.Background(), bashEvent(payload.EventPreToolUse, cmd, nil))
	if err != nil {
		t.Fatal(err)
	}
	if pre == nil || pre.HookSpecificOutput == nil || pre.HookSpecificOutput.PermissionDecision != payload.DecisionDeny {
		t.Fatalf("3 consecutive failures must deny, got %+v", pre)
	}
}

func TestLoopGuardSuccessResetsStreak(t *testing.T) {
	withIsolatedHome(t)
	const cmd = "pytest -x"

	for i := 0; i < 3; i++ {
		if _, err := loopGuard(context.Background(), bashEvent(payload.EventPostToolUse, cmd, responseWithExit(1))); err != nil {
			t.Fatal(err)
		}
	}
	// A success clears the ledger, so the next run is allowed.
	if _, err := loopGuard(context.Background(), bashEvent(payload.EventPostToolUse, cmd, responseWithExit(0))); err != nil {
		t.Fatal(err)
	}
	pre, err := loopGuard(context.Background(), bashEvent(payload.EventPreToolUse, cmd, nil))
	if err != nil {
		t.Fatal(err)
	}
	if pre != nil {
		t.Fatalf("a success must reset the streak, got %+v", pre)
	}
}

func TestLoopGuardDifferentCommandUnaffected(t *testing.T) {
	withIsolatedHome(t)
	const failing = "go test ./a"
	const other = "go test ./b"

	for i := 0; i < 3; i++ {
		if _, err := loopGuard(context.Background(), bashEvent(payload.EventPostToolUse, failing, responseWithExit(1))); err != nil {
			t.Fatal(err)
		}
	}
	pre, err := loopGuard(context.Background(), bashEvent(payload.EventPreToolUse, other, nil))
	if err != nil {
		t.Fatal(err)
	}
	if pre != nil {
		t.Fatalf("a different command must not be blocked, got %+v", pre)
	}
}

func TestLoopGuardInterruptedCountsAsFailure(t *testing.T) {
	withIsolatedHome(t)
	interrupted, _ := json.Marshal(map[string]bool{"interrupted": true})
	for i := 0; i < 3; i++ {
		if _, err := loopGuard(context.Background(), bashEvent(payload.EventPostToolUse, "npm install", interrupted)); err != nil {
			t.Fatal(err)
		}
	}
	pre, err := loopGuard(context.Background(), bashEvent(payload.EventPreToolUse, "npm install", nil))
	if err != nil {
		t.Fatal(err)
	}
	if pre == nil {
		t.Fatal("an interrupted run should count as a failure")
	}
}

func TestLoopGuardMissingExitCodeDoesNotRecord(t *testing.T) {
	withIsolatedHome(t)
	// No exit_code field: the ledger must not be touched.
	if _, err := loopGuard(context.Background(), bashEvent(payload.EventPostToolUse, "some cmd", json.RawMessage(`{"stdout":"x"}`))); err != nil {
		t.Fatal(err)
	}
	pre, err := loopGuard(context.Background(), bashEvent(payload.EventPreToolUse, "some cmd", nil))
	if err != nil {
		t.Fatal(err)
	}
	if pre != nil {
		t.Fatalf("an unrecorded run must not block, got %+v", pre)
	}
}

func TestLoopGuardStateFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no POSIX permission semantics")
	}
	withIsolatedHome(t)
	if _, err := loopGuard(context.Background(), bashEvent(payload.EventPostToolUse, "cmd x", responseWithExit(1))); err != nil {
		t.Fatal(err)
	}
	path, err := statePath()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("state file not written: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("state file mode = %#o, want 0600", mode)
	}
}

func TestParseExitCodeVariants(t *testing.T) {
	cases := []struct {
		name string
		raw  json.RawMessage
		want int
		ok   bool
	}{
		{"exit_code", json.RawMessage(`{"exit_code":1}`), 1, true},
		{"exitCode", json.RawMessage(`{"exitCode":2}`), 2, true},
		{"float exit_code", json.RawMessage(`{"exit_code":1.0}`), 1, true},
		{"interrupted", json.RawMessage(`{"interrupted":true}`), 130, true},
		{"missing", json.RawMessage(`{"stdout":"x"}`), 0, false},
		{"empty", nil, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseExitCode(tc.raw)
			if ok != tc.ok || (ok && got != tc.want) {
				t.Errorf("parseExitCode(%s) = %d, %v; want %d, %v", tc.raw, got, ok, tc.want, tc.ok)
			}
		})
	}
}
