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

	dst, err := installPlugin(false, true)
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

	dst, err := installPlugin(true, false)
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

// TestPluginInstallDirsDefaultSkipsHooks locks in the fix for the default
// `init` path: settings.json already registers the hooks, so the plugin
// installed at ~/.claude/plugins/claude-toolkit/ must NOT carry its own
// hooks/hooks.json -- Claude Code would otherwise load both and fire every
// event twice. The skills-* path keeps the full list.
func TestPluginInstallDirsDefaultSkipsHooks(t *testing.T) {
	def := pluginInstallDirs(false)
	for _, d := range def {
		if d == "hooks" {
			t.Errorf("default-init dirs must not include hooks/: %v", def)
		}
	}
	// Sanity: skills-* path still ships hooks/.
	withHooks := pluginInstallDirs(true)
	found := false
	for _, d := range withHooks {
		if d == "hooks" {
			found = true
		}
	}
	if !found {
		t.Errorf("skills-* dirs must include hooks/: %v", withHooks)
	}
}