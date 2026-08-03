package hooks

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zealot00/claude-toolkit/internal/payload"
)

func TestNotifyDisabledByDefault(t *testing.T) {
	t.Setenv("CLAUDE_TOOLKIT_NOTIFY", "")
	resp, err := notifier(context.Background(), bashEvent(payload.EventPreToolUse, "sleep 1", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp != nil {
		t.Errorf("disabled notifier should stay silent, got %+v", resp)
	}
}

func TestNotifyFiresOnSlowCall(t *testing.T) {
	withIsolatedHome(t)
	t.Setenv("CLAUDE_TOOLKIT_NOTIFY", "60")

	called := 0
	old := notifySend
	notifySend = func(string, string) { called++ }
	defer func() { notifySend = old }()

	// Record a start, then rewind the ledger so the elapsed time exceeds the
	// threshold without sleeping.
	if _, err := notifier(context.Background(), bashEvent(payload.EventPreToolUse, "sleep 1", nil)); err != nil {
		t.Fatal(err)
	}
	path, err := notifyStatePath()
	if err != nil {
		t.Fatal(err)
	}
	st := loadNotifyState(path)
	for k := range st.Timestamps {
		st.Timestamps[k] = time.Now().Add(-2 * time.Minute).UnixMilli()
	}
	saveNotifyState(path, st)

	resp, err := notifier(context.Background(), bashEvent(payload.EventPostToolUse, "sleep 1", responseWithExit(0)))
	if err != nil {
		t.Fatal(err)
	}
	if resp != nil {
		t.Errorf("notifier must not return a response, got %+v", resp)
	}
	if called != 1 {
		t.Errorf("expected 1 notification, got %d", called)
	}
}

func TestNotifyDoesNotFireOnFastCall(t *testing.T) {
	withIsolatedHome(t)
	t.Setenv("CLAUDE_TOOLKIT_NOTIFY", "60")

	called := 0
	old := notifySend
	notifySend = func(string, string) { called++ }
	defer func() { notifySend = old }()

	if _, err := notifier(context.Background(), bashEvent(payload.EventPreToolUse, "echo hi", nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := notifier(context.Background(), bashEvent(payload.EventPostToolUse, "echo hi", responseWithExit(0))); err != nil {
		t.Fatal(err)
	}
	if called != 0 {
		t.Errorf("fast successful call must not notify, got %d", called)
	}
}

func TestNotifyFiresOnFailure(t *testing.T) {
	withIsolatedHome(t)
	t.Setenv("CLAUDE_TOOLKIT_NOTIFY", "60")

	called := 0
	old := notifySend
	notifySend = func(string, string) { called++ }
	defer func() { notifySend = old }()

	if _, err := notifier(context.Background(), bashEvent(payload.EventPreToolUse, "false", nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := notifier(context.Background(), bashEvent(payload.EventPostToolUse, "false", responseWithExit(1))); err != nil {
		t.Fatal(err)
	}
	if called != 1 {
		t.Errorf("a failed call must notify regardless of duration, got %d", called)
	}
}

func TestNotifyStateMode(t *testing.T) {
	withIsolatedHome(t)
	t.Setenv("CLAUDE_TOOLKIT_NOTIFY", "60")
	if _, err := notifier(context.Background(), bashEvent(payload.EventPreToolUse, "x", nil)); err != nil {
		t.Fatal(err)
	}
	path, err := notifyStatePath()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("timestamp ledger not written: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("ledger mode = %#o, want 0600", mode)
	}
}

func TestNotifyKeyHidesInput(t *testing.T) {
	e := &payload.Event{Cwd: "/repo", ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"rm -rf /"}`)}
	key := notifyKey(e)
	if len(key) == 0 || len(key) > 128 {
		t.Fatalf("key = %q", key)
	}
	// The raw command must not appear in the key.
	if strings.Contains(key, "rm -rf") {
		t.Errorf("key leaks tool input: %q", key)
	}
}

func TestNotifyThresholdParsing(t *testing.T) {
	t.Setenv("CLAUDE_TOOLKIT_NOTIFY", "60")
	if d, ok := notifyThreshold(); !ok || d != 60*time.Second {
		t.Errorf("threshold = %v, %v; want 60s, true", d, ok)
	}
	t.Setenv("CLAUDE_TOOLKIT_NOTIFY", "0")
	if _, ok := notifyThreshold(); ok {
		t.Error("0 must mean disabled")
	}
	t.Setenv("CLAUDE_TOOLKIT_NOTIFY", "abc")
	if _, ok := notifyThreshold(); ok {
		t.Error("non-numeric must mean disabled")
	}
}

func TestNotifyStatePathIsolation(t *testing.T) {
	withIsolatedHome(t)
	p1, _ := notifyStatePath()
	withIsolatedHome(t)
	p2, _ := notifyStatePath()
	if filepath.Dir(filepath.Dir(p1)) == filepath.Dir(filepath.Dir(p2)) {
		t.Error("isolated homes must produce different state paths")
	}
}
