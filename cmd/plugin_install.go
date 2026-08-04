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
func installPlugin(dryRun bool) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dst := filepath.Join(home, ".claude", "plugins", "claude-toolkit")
	if dryRun {
		return dst, nil
	}
	if err := copyFS(pluginAssetsFS, dst, []string{".claude-plugin", "commands", "hooks"}); err != nil {
		return "", err
	}
	return dst, nil
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
	fmt.Println("Installed. Restart Claude Code for the /toolkit command and plugin hooks to load.")
	if scope == "skills-project" {
		fmt.Println("Note: project-scope plugins load only after the workspace is trusted.")
	}
	return 0
}
