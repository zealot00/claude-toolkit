// Package dir resolves the toolkit's own on-disk locations.
//
// The toolkit owns two layouts in priority order:
//
//  1. ${CLAUDE_PLUGIN_DATA} (or ~/.claude/plugins/data/claude-toolkit/ when
//     that env var is unset). Claude Code creates and deletes this directory
//     with the plugin lifecycle, so state here is cleaned up by
//     `/plugin uninstall` without a custom hook.
//  2. ~/.claude-toolkit/. The original home for the toolkit's private
//     state, kept as a legacy fallback. New writes go to the plugin-data
//     location; this one is only read so existing users do not lose
//     configuration on first run after the upgrade.
//
// Splitting toolkit state from Claude Code's own namespace (~/.claude/)
// keeps the user's settings file small and the toolkit's internals
// upgradeable without touching Claude Code.
//
// Layout under the resolved root:
//
//	<root>/
//	├── bin/claude-toolkit              (binary, when installed via plugin bin/)
//	├── state/                          (caches, reflogs)
//	└── logs/                           (debug logs)
package dir

import (
	"fmt"
	"os"
	"path/filepath"
)

// EnvHome is the legacy test override. Higher priority than CLAUDE_PLUGIN_DATA
// so test fixtures can pin the toolkit root without being shadowed by a plugin
// runtime that happens to be live.
const EnvHome = "CLAUDE_TOOLKIT_HOME"

// LegacyHome is the toolkit's original on-disk location, retained as a read
// fallback so existing users keep their state through the upgrade that moved
// the canonical location under ~/.claude/plugins/data/claude-toolkit/.
const LegacyHome = ".claude-toolkit"

// PluginHome is the canonical toolkit root when running as a Claude Code
// plugin. It is the directory Claude Code creates and removes with the plugin
// lifecycle (i.e. /plugin uninstall deletes it).
const PluginHome = ".claude/plugins/data/claude-toolkit"

// Root returns the toolkit root, creating it (0700) if it does not exist.
//
// Priority:
//
//  1. $CLAUDE_TOOLKIT_HOME (test override)
//  2. $CLAUDE_PLUGIN_DATA (Claude Code plugin runtime)
//  3. <home>/.claude/plugins/data/claude-toolkit/ (plugin fallback)
//  4. <home>/.claude-toolkit/ (legacy fallback)
//
// On first call after the upgrade, if priority 3 is empty and priority 4 has
// content, the legacy directory is atomically renamed into priority 3 so the
// user's existing state follows them to the new location.
func Root() (string, error) {
	// 1. test override
	if h := os.Getenv(EnvHome); h != "" {
		if err := os.MkdirAll(h, 0o700); err != nil {
			return "", fmt.Errorf("dir: create %s: %w", h, err)
		}
		return h, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("dir: locate home: %w", err)
	}

	// 2. plugin runtime (Claude Code injects this)
	if d := os.Getenv("CLAUDE_PLUGIN_DATA"); d != "" {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return "", fmt.Errorf("dir: create %s: %w", d, err)
		}
		return d, nil
	}

	// 3. plugin fallback + 4. legacy fallback
	pluginDir := filepath.Join(home, PluginHome)
	legacyDir := filepath.Join(home, LegacyHome)

	if err := migrateLegacyState(legacyDir, pluginDir); err != nil {
		// Migration failure is non-fatal: the legacy path still works for
		// reads, and the next call will retry the migration.
		debugfMigrate("migration skipped: %v", err)
	}

	// Prefer plugin dir; fall back to legacy if it cannot be created.
	if err := os.MkdirAll(pluginDir, 0o700); err == nil {
		return pluginDir, nil
	}
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		return "", fmt.Errorf("dir: create %s: %w", legacyDir, err)
	}
	return legacyDir, nil
}

// migrateLegacyState renames <legacy> into <plugin> atomically when the
// legacy directory has content but the plugin directory does not. It is
// idempotent: a second call after a successful migration is a no-op because
// the legacy directory no longer exists.
func migrateLegacyState(legacy, plugin string) error {
	if legacy == plugin {
		return nil
	}
	lInfo, err := os.Stat(legacy)
	if err != nil || !lInfo.IsDir() {
		return nil // no legacy state to migrate
	}
	pInfo, perr := os.Stat(plugin)
	if perr == nil && pInfo.IsDir() {
		// Plugin dir already exists. Only migrate if plugin dir is empty --
		// otherwise the user's two installations would silently merge.
		entries, _ := os.ReadDir(plugin)
		if len(entries) > 0 {
			return nil
		}
	}
	// Rename is atomic on POSIX when src and dst are on the same filesystem.
	// Both paths live under $HOME, so the rename stays on one device. The
	// destination's parent chain must already exist -- os.Rename does not
	// create parents like mkdir(2) does.
	if err := os.MkdirAll(filepath.Dir(plugin), 0o700); err != nil {
		return fmt.Errorf("prepare parent: %w", err)
	}
	debugfMigrate("rename %s -> %s", legacy, plugin)
	err = os.Rename(legacy, plugin)
	debugfMigrate("rename err=%v", err)
	return err
}

// Subdir returns <root>/<sub> with the root created and <sub> created with the
// given mode. The mode is applied with MkdirAll, which only sets it on the
// leaf directory; parents keep the mode Root established.
func Subdir(root, sub string, mode os.FileMode) (string, error) {
	full := filepath.Join(root, sub)
	if err := os.MkdirAll(full, mode); err != nil {
		return "", fmt.Errorf("dir: create %s: %w", full, err)
	}
	return full, nil
}

// Bin returns <root>/bin; the plugin layout places the binary here so the
// Bash tool's plugin-aware PATH picks it up.
func Bin(root string) string { return filepath.Join(root, "bin") }

// State returns <root>/state; caches, reflogs, and other derived data that
// can be deleted without losing user configuration.
func State(root string) string { return filepath.Join(root, "state") }

// Logs returns <root>/logs; reserved for future structured logging. Today the
// run-time debug log still lands in ~/.claude/claude-toolkit.log so the user
// can find it next to other Claude Code artifacts.
func Logs(root string) string { return filepath.Join(root, "logs") }

// debugfMigrate is a hook for tests to observe migration events. Production
// code is silent; tests opt in by overriding it.
var debugfMigrate = func(format string, args ...any) {}
