package hooks

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/zealot00/claude-toolkit/internal/payload"
	"github.com/zealot00/claude-toolkit/pkg/dir"
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
		{"ExitCode", json.RawMessage(`{"ExitCode":3}`), 3, true},
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

// TestLoopGuardFailureEvent: PostToolUseFailure is the official failure
// signal (no exit code in tool_response needed). Three of them must block.
func TestLoopGuardFailureEvent(t *testing.T) {
	withIsolatedHome(t)
	const cmd = "go test ./failing"

	for i := 0; i < 3; i++ {
		resp, err := loopGuard(context.Background(), bashEvent(payload.EventPostToolUseFailure, cmd, nil))
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
	if pre == nil || pre.HookSpecificOutput == nil || pre.HookSpecificOutput.PermissionDecision != payload.DecisionDeny {
		t.Fatalf("3 PostToolUseFailure events must deny, got %+v", pre)
	}
}

// TestLoopGuardFailureEventResetBySuccess: a successful PostToolUse (exit 0)
// after failure events resets the streak.
func TestLoopGuardFailureEventResetBySuccess(t *testing.T) {
	withIsolatedHome(t)
	const cmd = "make test"
	for i := 0; i < 3; i++ {
		if _, err := loopGuard(context.Background(), bashEvent(payload.EventPostToolUseFailure, cmd, nil)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := loopGuard(context.Background(), bashEvent(payload.EventPostToolUse, cmd, responseWithExit(0))); err != nil {
		t.Fatal(err)
	}
	pre, err := loopGuard(context.Background(), bashEvent(payload.EventPreToolUse, cmd, nil))
	if err != nil {
		t.Fatal(err)
	}
	if pre != nil {
		t.Fatalf("success must reset the failure streak, got %+v", pre)
	}
}

// TestLogFirstBashResponseIsIdempotent verifies that the tool_response dump
// is written once and never overwritten. The hook runs on every Bash
// PostToolUse — without idempotency the log would churn the disk and dilute
// the captured sample with later variants.
func TestLogFirstBashResponseIsIdempotent(t *testing.T) {
	withIsolatedHome(t)

	root, err := dir.Root()
	if err != nil {
		t.Fatalf("dir.Root: %v", err)
	}
	logDir := filepath.Join(root, "log")
	target := filepath.Join(logDir, "bash-response-fields.json")
	sentinel := filepath.Join(logDir, "bash-response-fields.json.sampled")

	first := responseWithExit(0)
	logFirstBashResponse(first)
	data1, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("first dump missing: %v", err)
	}
	sent1, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatalf("sentinel missing after first dump: %v", err)
	}

	// A different shape (exit code 1) — second call must NOT overwrite.
	second := responseWithExit(1)
	logFirstBashResponse(second)
	data2, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("second read: %v", err)
	}
	if string(data1) != string(data2) {
		t.Fatalf("dump was rewritten on second call — idempotency broken")
	}
	sent2, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatalf("sentinel missing after second dump: %v", err)
	}
	if string(sent1) != string(sent2) {
		t.Fatalf("sentinel was rewritten on second call — idempotency broken")
	}

	// Sanity: the dump is well-formed and lists the keys we put in.
	var parsed struct {
		Keys     []string          `json:"keys"`
		KeyTypes map[string]string `json:"key_types"`
	}
	if err := json.Unmarshal(data1, &parsed); err != nil {
		t.Fatalf("dump is not valid JSON: %v\n%s", err, data1)
	}
	if len(parsed.Keys) != 1 || parsed.Keys[0] != "exit_code" {
		t.Errorf("keys = %v, want [exit_code]", parsed.Keys)
	}
	if parsed.KeyTypes["exit_code"] != "number" {
		t.Errorf("key_types[exit_code] = %q, want \"number\"", parsed.KeyTypes["exit_code"])
	}
}
