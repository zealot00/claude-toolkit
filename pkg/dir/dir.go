// Package dir resolves the toolkit's own on-disk locations.
//
// The toolkit owns ~/.claude-toolkit/ for its private state (binary, config,
// scripts, caches) and writes only to ~/.claude/settings.json inside Claude
// Code's own namespace. Splitting them keeps the user's settings file small
// and the toolkit's internals upgradeable without touching Claude Code.
//
// Layout:
//
//	~/.claude-toolkit/
//	├── bin/claude-toolkit              (the binary itself, when --abs-path)
//	├── config.yaml                     (user overrides; future)
//	├── scripts/                        (auxiliary tools, e.g. AST helpers)
//	├── state/                          (caches, reflogs)
//	└── logs/                           (debug logs)
package dir

import (
	"fmt"
	"os"
	"path/filepath"
)

// Home is the toolkit root, conventionally ~/.claude-toolkit. It is the only
// location the toolkit considers its own; everything in ~/.claude/ belongs to
// Claude Code and must be merged with, not replaced.
const Home = ".claude-toolkit"

// EnvHome overrides Home when set, mainly for tests.
const EnvHome = "CLAUDE_TOOLKIT_HOME"

// Root returns the toolkit root, creating it (0700) if it does not exist.
// The 0700 mode matches the settings file's permissions: settings hold
// credentials, so does the directory that holds config and caches.
func Root() (string, error) {
	h := os.Getenv(EnvHome)
	if h == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("dir: locate home: %w", err)
		}
		h = filepath.Join(home, Home)
	}
	if err := os.MkdirAll(h, 0o700); err != nil {
		return "", fmt.Errorf("dir: create %s: %w", h, err)
	}
	return h, nil
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

// Bin returns <root>/bin; the install script places the binary here when the
// user chooses the per-user layout.
func Bin(root string) string { return filepath.Join(root, "bin") }

// Config returns <root>/config.yaml. The file may not exist yet; callers that
// write it should be careful not to clobber user-edited content.
func Config(root string) string { return filepath.Join(root, "config.yaml") }

// Scripts returns <root>/scripts; the location for helper binaries the
// toolkit shells out to (e.g. a Python AST extractor if we add one).
func Scripts(root string) string { return filepath.Join(root, "scripts") }

// Logs returns <root>/logs; reserved for future structured logging. Today the
// run-time debug log still lands in ~/.claude/claude-toolkit.log so the user
// can find it next to other Claude Code artifacts.
func Logs(root string) string { return filepath.Join(root, "logs") }

// State returns <root>/state; caches, reflogs, and other derived data that
// can be deleted without losing user configuration.
func State(root string) string { return filepath.Join(root, "state") }
