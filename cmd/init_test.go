package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInjectUserStatusLine_InjectsWhenAbsent verifies that running
// `claude-toolkit init --scope=skills-user` (the recommended plugin
// install surface) also writes a default statusLine into
// ~/.claude/settings.json. Without this, HUD never renders: Claude Code
// reads statusLine from user-level settings, not from the plugin
// manifest.
func TestInjectUserStatusLine_InjectsWhenAbsent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	// Must NOT inherit CLAUDE_TOOLKIT_HOME -- the helper should always
	// write to $HOME/.claude/settings.json regardless of the toolkit's
	// own state path.
	t.Setenv("CLAUDE_TOOLKIT_HOME", "")
	t.Setenv("CLAUDE_PLUGIN_DATA", "")

	if err := injectUserStatusLine(false); err != nil {
		t.Fatalf("injectUserStatusLine: %v", err)
	}

	settingsPath := filepath.Join(home, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("settings.json not written: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("settings.json is not valid JSON: %v\n%s", err, data)
	}
	sl, ok := root["statusLine"].(map[string]any)
	if !ok {
		t.Fatalf("statusLine missing or wrong type: %v", root["statusLine"])
	}
	if sl["type"] != "command" {
		t.Errorf("statusLine.type = %v, want \"command\"", sl["type"])
	}
	cmd, ok := sl["command"].(string)
	if !ok {
		t.Fatalf("statusLine.command missing or wrong type: %v", sl["command"])
	}
	if !strings.HasSuffix(cmd, " hud") {
		t.Errorf("statusLine.command = %q, want suffix \" hud\"", cmd)
	}
}

// TestInjectUserStatusLine_KeepsExisting verifies the helper refuses to
// overwrite a user's existing statusLine (the design contract from
// PLAN.md §7.2: HUD is opt-out, not opt-in).
func TestInjectUserStatusLine_KeepsExisting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLAUDE_TOOLKIT_HOME", "")
	t.Setenv("CLAUDE_PLUGIN_DATA", "")

	settingsPath := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	existing := `{
  "statusLine": {"type": "command", "command": "my-custom-status"},
  "model": "opus"
}`
	if err := os.WriteFile(settingsPath, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := injectUserStatusLine(false); err != nil {
		t.Fatalf("injectUserStatusLine: %v", err)
	}

	data, _ := os.ReadFile(settingsPath)
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	sl := root["statusLine"].(map[string]any)
	if sl["command"] != "my-custom-status" {
		t.Errorf("statusLine was overwritten: command = %v, want preserved", sl["command"])
	}
	if root["model"] != "opus" {
		t.Errorf("sibling field model = %v, want preserved", root["model"])
	}
}

// TestInjectUserStatusLine_DryRunDoesNotWrite verifies dryRun=true does
// not touch disk. The plan's Changed() reporting still drives the
// summary line the user sees, but the file is left alone.
func TestInjectUserStatusLine_DryRunDoesNotWrite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLAUDE_TOOLKIT_HOME", "")
	t.Setenv("CLAUDE_PLUGIN_DATA", "")

	if err := injectUserStatusLine(true); err != nil {
		t.Fatalf("injectUserStatusLine (dry-run): %v", err)
	}

	settingsPath := filepath.Join(home, ".claude", "settings.json")
	if _, err := os.Stat(settingsPath); !os.IsNotExist(err) {
		t.Errorf("dry-run must not create %s; stat err = %v", settingsPath, err)
	}
}

// TestInitSkillsScope_AlsoInjectsStatusLine is the integration test for
// the wiring fix: running `claude-toolkit init --scope=skills-user` must
// (a) install the plugin payload and (b) inject statusLine into the
// user's settings.json. Without (b), HUD silently never renders.
//
// Skipped when the embedded plugin payload is missing (e.g. when the
// plugin_assets.go file was absent at compile time, mirroring the
// existing pattern in plugin_install_test.go).
func TestInitSkillsScope_AlsoInjectsStatusLine(t *testing.T) {
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
	t.Setenv("CLAUDE_TOOLKIT_HOME", "")
	t.Setenv("CLAUDE_PLUGIN_DATA", "")

	if rc := initSkillsScope("skills-user", home, false); rc != 0 {
		t.Fatalf("initSkillsScope returned %d", rc)
	}

	pluginDir := filepath.Join(home, ".claude", "skills", "claude-toolkit")
	if _, err := os.Stat(filepath.Join(pluginDir, "hooks", "hooks.json")); err != nil {
		t.Errorf("plugin hooks.json missing: %v", err)
	}

	settingsPath := filepath.Join(home, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("settings.json not written: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	if _, ok := root["statusLine"]; !ok {
		t.Errorf("statusLine missing from settings.json after initSkillsScope:\n%s", data)
	}
}
