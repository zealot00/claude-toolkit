package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

// TestInstallPluginCopiesTree verifies that installPlugin copies every
// directory in the embedded payload (.claude-plugin/, commands/, hooks/)
// from the binary's embed.FS into the destination under the user's HOME.
func TestInstallPluginCopiesTree(t *testing.T) {
	// Skip cleanly when the test binary was built without the embed (e.g.,
	// if the plugin_assets.go file was missing at compile time).
	fs := PluginAssets()
	for _, want := range []string{
		".claude-plugin/plugin.json",
		"commands/toolkit.md",
		"hooks/hooks.json",
	} {
		if _, err := fs.ReadFile(want); err != nil {
			t.Skipf("embedded payload missing %s: %v", want, err)
		}
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	dst, err := installPlugin(false)
	if err != nil {
		t.Fatalf("installPlugin: %v", err)
	}
	wantDst := filepath.Join(home, ".claude", "plugins", "claude-toolkit")
	if dst != wantDst {
		t.Errorf("dst = %q, want %q", dst, wantDst)
	}
	for _, rel := range []string{
		".claude-plugin/plugin.json",
		"commands/toolkit.md",
		"hooks/hooks.json",
	} {
		if _, err := os.Stat(filepath.Join(dst, rel)); err != nil {
			t.Errorf("missing copied file %s: %v", rel, err)
		}
	}
}

// TestInstallPluginDryRun verifies that --dry-run returns the destination
// without writing anything.
func TestInstallPluginDryRun(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	dst, err := installPlugin(true)
	if err != nil {
		t.Fatalf("installPlugin dry-run: %v", err)
	}
	wantDst := filepath.Join(home, ".claude", "plugins", "claude-toolkit")
	if dst != wantDst {
		t.Errorf("dst = %q, want %q", dst, wantDst)
	}
	if _, err := os.Stat(wantDst); err == nil {
		t.Errorf("dry-run must not create %s", wantDst)
	}
}