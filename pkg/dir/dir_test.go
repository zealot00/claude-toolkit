package dir

import (
	"os"
	"path/filepath"
	"testing"
)

// withCleanEnv clears the env vars Root() consults so each test starts from
// the same baseline. CLAUDE_TOOLKIT_HOME wins over everything, so a
// leftover value would mask the path under test.
func withCleanEnv(t *testing.T) {
	t.Helper()
	t.Setenv(EnvHome, "")
	t.Setenv("CLAUDE_PLUGIN_DATA", "")
}

// TestRootReturnsPluginDataByDefault confirms that when no plugin runtime and
// no CLAUDE_TOOLKIT_HOME are present, Root() returns the plugin-data
// directory. ~/.claude-toolkit/ is no longer the canonical location; it is
// only read for one-shot migration of existing user state.
func TestRootReturnsPluginDataByDefault(t *testing.T) {
	withCleanEnv(t)

	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	root, err := Root()
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	want := filepath.Join(tmp, PluginHome)
	if root != want {
		t.Errorf("Root = %q, want plugin-data dir %q", root, want)
	}
}

// TestRootRespectsPluginRuntime confirms CLAUDE_PLUGIN_DATA wins over the
// legacy fallback so /plugin uninstall cleans up the right directory.
func TestRootRespectsPluginRuntime(t *testing.T) {
	withCleanEnv(t)

	tmp := t.TempDir()
	t.Setenv("CLAUDE_PLUGIN_DATA", filepath.Join(tmp, "plugin-data"))

	root, err := Root()
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	want := filepath.Join(tmp, "plugin-data")
	if root != want {
		t.Errorf("Root = %q, want %q", root, want)
	}
}

// TestRootRespectsTestOverride confirms CLAUDE_TOOLKIT_HOME still wins over
// every other signal -- it is the test fixture pin documented in the package
// comment.
func TestRootRespectsTestOverride(t *testing.T) {
	withCleanEnv(t)

	tmp := t.TempDir()
	t.Setenv(EnvHome, filepath.Join(tmp, "fixture"))

	root, err := Root()
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	want := filepath.Join(tmp, "fixture")
	if root != want {
		t.Errorf("Root = %q, want %q", root, want)
	}
}

// TestRootMigratesLegacyState confirms a populated ~/.claude-toolkit/ is
// atomically moved to the plugin-data directory the first time Root() runs
// after the upgrade, so existing users do not lose state.
func TestRootMigratesLegacyState(t *testing.T) {
	withCleanEnv(t)

	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	legacy := filepath.Join(tmp, LegacyHome)
	if err := os.MkdirAll(filepath.Join(legacy, "state"), 0o700); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "state", "cap.json"), []byte(`{"enabled":{"guard":true}}`), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	root, err := Root()
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	pluginDir := filepath.Join(tmp, PluginHome)
	if root != pluginDir {
		t.Errorf("Root = %q, want migrated %q", root, pluginDir)
	}
	if _, err := os.Stat(filepath.Join(pluginDir, "state", "cap.json")); err != nil {
		t.Errorf("migrated file missing: %v", err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Errorf("legacy still present after migration: err=%v", err)
	}
}

// TestRootMigrationIsIdempotent confirms a second Root() call after a
// successful migration is a no-op and does not error.
func TestRootMigrationIsIdempotent(t *testing.T) {
	withCleanEnv(t)

	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	legacy := filepath.Join(tmp, LegacyHome)
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatalf("seed: %v", err)
	}

	for i := range 3 {
		if _, err := Root(); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
}

// TestRootDoesNotClobberPluginData confirms that when the plugin-data
// directory already exists with content, a legacy directory is NOT migrated
// on top of it -- otherwise two parallel installations would silently merge.
func TestRootDoesNotClobberPluginData(t *testing.T) {
	withCleanEnv(t)

	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	// Plugin dir exists with content first.
	pluginDir := filepath.Join(tmp, PluginHome)
	if err := os.MkdirAll(pluginDir, 0o700); err != nil {
		t.Fatalf("seed plugin: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "marker"), []byte("plugin"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	// Then legacy dir appears.
	legacy := filepath.Join(tmp, LegacyHome)
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "marker"), []byte("legacy"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	root, err := Root()
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	if root != pluginDir {
		t.Errorf("Root = %q, want plugin dir %q", root, pluginDir)
	}
	data, err := os.ReadFile(filepath.Join(pluginDir, "marker"))
	if err != nil {
		t.Fatalf("read plugin marker: %v", err)
	}
	if string(data) != "plugin" {
		t.Errorf("plugin marker = %q, want %q (legacy clobbered it)", data, "plugin")
	}
}
