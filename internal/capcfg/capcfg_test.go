package capcfg

import (
	"os"
	"path/filepath"
	"testing"
)

// withIsolatedHome points CLAUDE_TOOLKIT_HOME at a temp dir.
func withIsolatedHome(t *testing.T) {
	t.Helper()
	t.Setenv("CLAUDE_TOOLKIT_HOME", filepath.Join(t.TempDir(), "home"))
}

func TestLoadMissingMeansAllEnabled(t *testing.T) {
	withIsolatedHome(t)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Enabled) != 0 {
		t.Errorf("missing config must mean all enabled, got %v", c.Enabled)
	}
	d, err := Disabled()
	if err != nil || len(d) != 0 {
		t.Errorf("Disabled() = %v, %v; want empty", d, err)
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	withIsolatedHome(t)
	enabled := map[string]bool{"guard": true, "format": false, "loopguard": true}
	if err := Save(enabled); err != nil {
		t.Fatal(err)
	}
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !c.Enabled["guard"] || c.Enabled["format"] || !c.Enabled["loopguard"] {
		t.Errorf("round-trip mismatch: %v", c.Enabled)
	}
	d, err := Disabled()
	if err != nil {
		t.Fatal(err)
	}
	if len(d) != 1 || !d["format"] {
		t.Errorf("Disabled() = %v, want only format", d)
	}
}

func TestCorruptConfigDefaultsToAllEnabled(t *testing.T) {
	withIsolatedHome(t)
	path, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load()
	if err != nil {
		t.Fatalf("corrupt config must not error: %v", err)
	}
	if len(c.Enabled) != 0 {
		t.Errorf("corrupt config must mean all enabled, got %v", c.Enabled)
	}
}

func TestSaveFileMode(t *testing.T) {
	if os.Getenv("CI") == "" && os.Getenv("GOOS") == "windows" {
		t.Skip("Windows has no POSIX permission semantics")
	}
	withIsolatedHome(t)
	if err := Save(map[string]bool{"guard": true}); err != nil {
		t.Fatal(err)
	}
	path, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("mode = %#o, want 0600", mode)
	}
}
