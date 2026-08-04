package cmd

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// installPlugin copies the embedded Claude Code plugin payload
// (.claude-plugin/, commands/, hooks/) into ~/.claude/plugins/claude-toolkit/
// so the /toolkit command works right after `init`, without a manual copy.
//
// The source is the embed.FS main() wires into cmd via SetPluginAssets at
// startup, so the binary is self-contained: `go install` users get the same
// install flow as developers running from a checkout.
// installPlugin copies the embedded Claude Code plugin payload into
// ~/.claude/plugins/claude-toolkit/ so the /toolkit command works right after
// `init`, without a manual copy.
//
// includeHooks=false is the default for the user/project/local init path:
// hooks for that path live in settings.json, and shipping them inside the
// plugin too would make Claude Code fire every event twice. Pass true only
// when the install path is the skills-directory plugin (initSkillsScope),
// where settings.json is never written and the plugin's own hooks/hooks.json
// is the only hook registration.
//
// The source is the embed.FS main() wires into cmd via SetPluginAssets at
// startup, so the binary is self-contained: `go install` users get the same
// install flow as developers running from a checkout.
func installPlugin(dryRun bool, includeHooks bool) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dst := filepath.Join(home, ".claude", "plugins", "claude-toolkit")
	dirs := pluginInstallDirs(includeHooks)
	if dryRun {
		return dst, nil
	}
	if err := copyFS(pluginAssetsFS, dst, dirs); err != nil {
		return "", err
	}
	return dst, nil
}

// pluginInstallDirs returns the embedded directories that installPlugin
// should copy. The default-init path (user/project/local) skips hooks/:
// settings.json already registers the hooks and shipping them inside the
// plugin too would make Claude Code fire every event twice. The skills-*
// path (initSkillsScope) calls copyFS directly with the full list.
func pluginInstallDirs(includeHooks bool) []string {
	dirs := []string{".claude-plugin", "commands"}
	if includeHooks {
		dirs = append(dirs, "hooks")
	}
	return dirs
}

// copyFS copies a fixed list of top-level directories from src (an embed.FS)
// into dst on the real filesystem, preserving the directory layout. The
// top-level name itself becomes a subdirectory under dst (so the installed
// tree mirrors the source tree, which Claude Code requires).
func copyFS(src embed.FS, dst string, dirs []string) error {
	for _, dir := range dirs {
		root := fs.FS(src)
		err := fs.WalkDir(root, dir, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			rel, err := filepath.Rel(dir, path)
			if err != nil {
				return err
			}
			target := filepath.Join(dst, dir, rel)
			if d.IsDir() {
				return os.MkdirAll(target, 0o755)
			}
			data, err := fs.ReadFile(root, path)
			if err != nil {
				return err
			}
			return os.WriteFile(target, data, 0o644)
		})
		if err != nil {
			return fmt.Errorf("copy %s: %w", dir, err)
		}
	}
	return nil
}

// pluginHooksInstalled reports whether the Claude Code plugin's own
// hooks/hooks.json is present (copied by init or installed by the user).
// Writing the same hooks into settings.json as well would fire every event
// twice, so init warns about it.
func pluginHooksInstalled() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	for _, p := range []string{
		filepath.Join(home, ".claude", "plugins", "claude-toolkit", "hooks", "hooks.json"),
		filepath.Join(home, ".claude", "skills", "claude-toolkit", "hooks", "hooks.json"),
	} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// initSkillsScope installs the plugin into a skills-directory location, which
// Claude Code auto-loads: skills-user is global (~/.claude/skills/
// claude-toolkit/), skills-project is per-project (<dir>/.claude/skills/
// claude-toolkit/, requires workspace trust).
//
// The plugin's hooks/hooks.json references ${CLAUDE_PLUGIN_ROOT}/bin/
// claude-toolkit, so the binary is copied alongside the manifest under
// bin/. Claude Code adds the plugin's bin/ to the Bash tool's PATH while
// the plugin is enabled, so this avoids depending on the user's shell
// PATH -- a Mac GUI-launched Claude Code without inherited shell PATH
// still finds the binary.
func initSkillsScope(scope, projectDir string, dryRun bool) int {
	var dst string
	if scope == "skills-user" {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		dst = filepath.Join(home, ".claude", "skills", "claude-toolkit")
	} else {
		dst = filepath.Join(projectDir, ".claude", "skills", "claude-toolkit")
	}

	fmt.Printf("Installing the claude-toolkit plugin (skills scope) into %s\n\n", dst)
	if dryRun {
		fmt.Println("(dry run -- nothing written)")
		return 0
	}
	if err := copyFS(pluginAssetsFS, dst, []string{".claude-plugin", "commands", "hooks"}); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if err := installBinary(dst); err != nil {
		// A missing or un-copyable binary is a warning, not a fatal error:
		// the plugin manifest and commands are already in place, and the
		// user may have intentionally arranged for the binary to live
		// elsewhere on PATH.
		fmt.Fprintf(os.Stderr, "warning: could not copy binary into plugin bin/: %v\n", err)
	} else {
		fmt.Println("Installed. Restart Claude Code for the /toolkit command and plugin hooks to load.")
	}
	if scope == "skills-project" {
		fmt.Println("Note: project-scope plugins load only after the workspace is trusted.")
	}
	return 0
}

// installBinary copies the currently-running claude-toolkit executable into
// <dst>/bin/ and chmods it 0755. The destination's bin/ directory is added
// to the Bash tool's PATH by Claude Code while the plugin is enabled, which
// makes the ${CLAUDE_PLUGIN_ROOT}/bin/claude-toolkit reference inside
// hooks/hooks.json resolve without depending on the user's shell PATH.
func installBinary(dst string) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate self: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}
	src, err := os.Open(self)
	if err != nil {
		return fmt.Errorf("open %s: %w", self, err)
	}
	defer src.Close()

	binDir := filepath.Join(dst, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", binDir, err)
	}
	dstPath := filepath.Join(binDir, "claude-toolkit")
	dstFile, err := os.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("open %s: %w", dstPath, err)
	}
	defer dstFile.Close()
	if _, err := src.WriteTo(dstFile); err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	return nil
}
