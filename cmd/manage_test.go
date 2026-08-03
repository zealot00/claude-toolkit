package cmd

import (
	"path/filepath"
	"testing"

	"github.com/zealot00/claude-toolkit/pkg/installer"
)

// TestManageRoundTrip installs every capability, disables one, verifies the
// settings file reflects it, then re-enables it. This is the core of what the
// /toolkit plugin command does inside Claude Code.
func TestManageRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")

	apply := func(enabled map[string]bool) {
		t.Helper()
		plan, err := capabilityPlan(path, "claude-toolkit", enabled)
		if err != nil {
			t.Fatalf("capabilityPlan: %v", err)
		}
		if err := plan.Apply(); err != nil {
			t.Fatalf("Apply: %v", err)
		}
	}

	all := map[string]bool{"guard": true, "format": true, "enrich": true}
	apply(all)

	entries, err := installer.Inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("want 3 installed capabilities after full install, got %d: %+v", len(entries), entries)
	}

	// Disable guard: its matcher group must disappear, siblings stay.
	apply(map[string]bool{"format": true, "enrich": true})
	entries, err = installer.Inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 entries after disabling guard, got %d: %+v", len(entries), entries)
	}
	for _, e := range entries {
		if e.Capability == "guard" {
			t.Error("guard should be absent after disable")
		}
		if e.Capability == "" {
			t.Errorf("entry %q carries no capability tag", e.Command)
		}
	}

	// Re-enable guard.
	apply(all)
	entries, err = installer.Inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("want 3 entries after re-enabling guard, got %d", len(entries))
	}
}

// TestCapabilityPlanNoopWhenUnchanged: re-applying the same set must produce
// an unmodified plan, or re-running `manage` would churn the settings file.
func TestCapabilityPlanNoopWhenUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	all := map[string]bool{"guard": true, "format": true, "enrich": true}

	plan, err := capabilityPlan(path, "claude-toolkit", all)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}

	again, err := capabilityPlan(path, "claude-toolkit", all)
	if err != nil {
		t.Fatal(err)
	}
	if again.Changed() {
		t.Error("re-applying the same capability set reports a change")
	}
}

// TestManageListNeverErrorsOnMissingFile: listing before init must be a
// friendly no-op, not a failure -- Claude runs `manage list` first thing.
func TestManageListNeverErrorsOnMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent", "settings.json")
	if fileExists(path) {
		t.Fatal("fixture setup error: file should not exist")
	}
	enabled, err := enabledCapabilities(path)
	if err != nil {
		t.Fatalf("enabledCapabilities on a missing file must not error: %v", err)
	}
	if len(enabled) != 0 {
		t.Errorf("want no enabled capabilities, got %v", enabled)
	}
}

// TestLegacyCapabilitiesDetectsUntaggedEntries: installs written before
// --cap tags must be countable so manage can refuse to write over them.
func TestLegacyCapabilitiesDetectsUntaggedEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	// A pre-capability install: same shape, but commands carry no --cap.
	plan, err := installer.BuildPlan(path, []installer.Spec{
		{Event: "PreToolUse", Matcher: "Bash|Write", Command: "claude-toolkit run --event=pre", Timeout: 10},
		{Event: "SessionStart", Matcher: "*", Command: "claude-toolkit run --event=session", Timeout: 15},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}

	if n, err := legacyCapabilities(path); err != nil || n != 2 {
		t.Fatalf("legacyCapabilities = %d, %v; want 2 untagged entries", n, err)
	}
	enabled, err := enabledCapabilities(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(enabled) != 0 {
		t.Errorf("untagged entries must not count as enabled capabilities: %v", enabled)
	}
}

// TestManageDisableAll pins the empty-set semantics: disabling every
// capability must remove every toolkit entry, not re-enable them all via
// buildSpecs' "empty caps = install all" contract.
func TestManageDisableAll(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	plan, err := capabilityPlan(path, "claude-toolkit", map[string]bool{"guard": true, "format": true, "enrich": true})
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}

	allDisabled := map[string]bool{"guard": false, "format": false, "enrich": false}
	plan, err = capabilityPlan(path, "claude-toolkit", allDisabled)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Changed() {
		t.Fatal("disabling everything must produce a change")
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}

	entries, err := installer.Inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("disable-all left %d entries behind: %+v", len(entries), entries)
	}

	// And re-enabling from scratch works after a full disable.
	plan, err = capabilityPlan(path, "claude-toolkit", map[string]bool{"guard": true})
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	entries, err = installer.Inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Capability != "guard" {
		t.Fatalf("re-enable after disable-all = %+v, want exactly guard", entries)
	}
}

// TestEnabledKeysPinsDisabledSemantics: a capability disabled in the map
// (value false) must not leak into the buildSpecs list, or `manage disable`
// would silently re-enable it.
func TestEnabledKeysPinsDisabledSemantics(t *testing.T) {
	got := enabledKeys(map[string]bool{"guard": true, "format": false, "enrich": true})
	if len(got) != 2 || got[0] != "enrich" || got[1] != "guard" {
		t.Fatalf("enabledKeys = %v, want [enrich guard] (sorted, false excluded)", got)
	}
}

// TestManageDisableRoundTrip is the end-to-end disable path with a dirty map
// (both true and false values), matching what the CLI builds after toggling.
func TestManageDisableRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	plan, err := capabilityPlan(path, "claude-toolkit", map[string]bool{"guard": true, "format": true, "enrich": true})
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}

	// Toggle format off the way the CLI does: read state, flip, recompute.
	enabled, err := enabledCapabilities(path)
	if err != nil {
		t.Fatal(err)
	}
	enabled["format"] = false
	plan, err = capabilityPlan(path, "claude-toolkit", enabled)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Changed() {
		t.Fatal("disabling an enabled capability must produce a change")
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}

	entries, err := installer.Inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 entries after disabling format, got %d: %+v", len(entries), entries)
	}
	for _, e := range entries {
		if e.Capability == "format" {
			t.Error("format should be disabled (absent from settings)")
		}
	}
}
