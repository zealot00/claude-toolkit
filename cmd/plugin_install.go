package cmd

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime/debug"
)

// installPlugin copies the bundled Claude Code plugin (this repo's
// .claude-plugin/ and commands/) into ~/.claude/plugins/claude-toolkit/ so
// the /toolkit command works right after `init`, without a manual copy.
//
// The source is located either from the source tree (exe-relative: a checkout
// whose binary sits in bin/ or the repo root) or from the Go module cache
// (`go install` keeps the module source there under GOMODCACHE). Returns the
// destination, or an error explaining why the plugin could not be installed.
func installPlugin(dryRun bool) (string, error) {
	src, err := findPluginSource()
	if err != nil {
		return "", err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dst := filepath.Join(home, ".claude", "plugins", "claude-toolkit")
	if dryRun {
		return dst, nil
	}
	if err := copyDir(filepath.Join(src, ".claude-plugin"), filepath.Join(dst, ".claude-plugin")); err != nil {
		return "", fmt.Errorf("copy .claude-plugin: %w", err)
	}
	if err := copyDir(filepath.Join(src, "commands"), filepath.Join(dst, "commands")); err != nil {
		return "", fmt.Errorf("copy commands: %w", err)
	}
	return dst, nil
}

// osExecutable is indirected so tests can point the search at a temp tree.
var osExecutable = os.Executable

// findPluginSource returns the plugin root (a directory containing
// .claude-plugin/plugin.json), or an error.
func findPluginSource() (string, error) {
	// 1. Exe-relative: covers a checkout whose binary lives in bin/ or the
	// repo root.
	if exe, err := osExecutable(); err == nil {
		if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
			exe = resolved
		}
		for d := filepath.Dir(exe); ; {
			if _, err := os.Stat(filepath.Join(d, ".claude-plugin", "plugin.json")); err == nil {
				return d, nil
			}
			parent := filepath.Dir(d)
			if parent == d {
				break
			}
			d = parent
		}
	}
	// 2. Module cache: `go install` keeps the module source under GOMODCACHE.
	if info, ok := debug.ReadBuildInfo(); ok {
		v := info.Main.Version
		if v != "" && v != "(devel)" {
			candidate := filepath.Join(moduleCacheRoot(), "github.com", "zealot00", "claude-toolkit@"+v)
			if _, err := os.Stat(filepath.Join(candidate, ".claude-plugin", "plugin.json")); err == nil {
				return candidate, nil
			}
		}
	}
	return "", fmt.Errorf(
		"could not locate the plugin source (.claude-plugin/) next to the binary or in the module cache;\n" +
			"install it manually with /plugin in Claude Code, or copy .claude-plugin/ and commands/ by hand")
}

func moduleCacheRoot() string {
	if v := os.Getenv("GOMODCACHE"); v != "" {
		return v
	}
	if gopath := os.Getenv("GOPATH"); gopath != "" {
		return filepath.Join(gopath, "pkg", "mod")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, "go", "pkg", "mod")
	}
	return ""
}

// copyDir copies src into dst, creating dst and preserving a simple file
// layout. It is used for the small plugin tree; not a general-purpose tool.
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}
