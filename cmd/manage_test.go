package cmd

import (
	"path/filepath"
	"testing"

	"github.com/zealot00/claude-toolkit/internal/capcfg"
)

func withIsolatedHome(t *testing.T) {
	t.Helper()
	t.Setenv("CLAUDE_TOOLKIT_HOME", filepath.Join(t.TempDir(), "home"))
}

// allCapNames returns every registered capability name.
func allCapNames() []string {
	caps := registeredCapabilities()
	out := make([]string, 0, len(caps))
	for _, c := range caps {
		out = append(out, c.name)
	}
	return out
}

// TestManageRoundTrip enables everything, disables guard via the capcfg path,
// verifies the state, then re-enables it. This is the core of what the
// /toolkit plugin command does inside Claude Code.
func TestManageRoundTrip(t *testing.T) {
	withIsolatedHome(t)

	if code := manageSetAll(true, false); code != 0 {
		t.Fatalf("enable-all exit = %d", code)
	}
	disabled, err := capcfg.Disabled()
	if err != nil {
		t.Fatal(err)
	}
	if len(disabled) != 0 {
		t.Fatalf("after enable-all nothing should be disabled, got %v", disabled)
	}

	if code := manageSet([]string{"guard"}, false, false); code != 0 {
		t.Fatalf("disable guard exit = %d", code)
	}
	disabled, err = capcfg.Disabled()
	if err != nil {
		t.Fatal(err)
	}
	if len(disabled) != 1 || !disabled["guard"] {
		t.Fatalf("disabled = %v, want exactly guard", disabled)
	}

	if code := manageSet([]string{"guard"}, true, false); code != 0 {
		t.Fatalf("re-enable guard exit = %d", code)
	}
	disabled, err = capcfg.Disabled()
	if err != nil {
		t.Fatal(err)
	}
	if len(disabled) != 0 {
		t.Fatalf("after re-enable nothing should be disabled, got %v", disabled)
	}
}

// TestManageDisableAll pins the all-off state.
func TestManageDisableAll(t *testing.T) {
	withIsolatedHome(t)
	if code := manageSetAll(false, false); code != 0 {
		t.Fatalf("disable-all exit = %d", code)
	}
	disabled, err := capcfg.Disabled()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range allCapNames() {
		if !disabled[n] {
			t.Errorf("capability %q still enabled after disable-all", n)
		}
	}
}

// TestManageUnknownCapabilityRejected: a bad name is a usage error, not a
// state change.
func TestManageUnknownCapabilityRejected(t *testing.T) {
	withIsolatedHome(t)
	if code := manageSet([]string{"no-such-cap"}, true, false); code != 2 {
		t.Errorf("unknown capability exit = %d, want 2", code)
	}
}

// TestManageListNeverErrorsOnMissingState: listing before any manage call is
// a friendly all-enabled default, not a failure.
func TestManageListNeverErrorsOnMissingState(t *testing.T) {
	withIsolatedHome(t)
	enabled, err := enabledCapabilities()
	if err != nil {
		t.Fatalf("enabledCapabilities on missing state must not error: %v", err)
	}
	if len(enabled) != len(allCapNames()) {
		t.Errorf("missing state must mean all enabled, got %d of %d", len(enabled), len(allCapNames()))
	}
	for _, on := range enabled {
		if !on {
			t.Error("missing state must mean every capability enabled")
		}
	}
}

// TestEnabledKeysPinsDisabledSemantics: disabled capabilities are excluded
// from the key list used for messages.
func TestEnabledKeysPinsDisabledSemantics(t *testing.T) {
	got := enabledKeys(map[string]bool{"guard": true, "format": false, "enrich": true})
	if len(got) != 2 || got[0] != "enrich" || got[1] != "guard" {
		t.Fatalf("enabledKeys = %v, want [enrich guard]", got)
	}
}

// TestManageDryRunDoesNotWrite: --dry-run must not touch the state file.
func TestManageDryRunDoesNotWrite(t *testing.T) {
	withIsolatedHome(t)
	if code := manageSet([]string{"guard"}, false, true); code != 0 {
		t.Fatalf("dry-run exit = %d", code)
	}
	disabled, err := capcfg.Disabled()
	if err != nil {
		t.Fatal(err)
	}
	if len(disabled) != 0 {
		t.Errorf("dry-run must not write state, got %v", disabled)
	}
}
