package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zealot00/claude-toolkit/internal/profiles"
	"github.com/zealot00/claude-toolkit/pkg/dir"
)

// modelEnv isolates HOME (settings.json) and CLAUDE_TOOLKIT_HOME (profiles).
func modelEnv(t *testing.T) string {
	root := t.TempDir()
	t.Setenv(dir.EnvHome, root)
	t.Setenv("HOME", root)
	return root
}

func runModel(t *testing.T, args ...string) (string, int) {
	t.Helper()
	oldOut, oldErr := os.Stdout, os.Stderr
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	os.Stdout, os.Stderr = wOut, wErr
	code := modelCommand(args)
	wOut.Close()
	wErr.Close()
	os.Stdout, os.Stderr = oldOut, oldErr
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, rOut)
	_, _ = io.Copy(&buf, rErr)
	return buf.String(), code
}

func TestModelAddUseListRoundTrip(t *testing.T) {
	modelEnv(t)

	if _, code := runModel(t, "add", "--name", "mini",
		"--base-url", "https://api.minimaxi.com/anthropic",
		"--token", "sk-test", "--model", "MiniMax-M3"); code != 0 {
		t.Fatalf("add failed: %d", code)
	}

	out, code := runModel(t, "list")
	if code != 0 {
		t.Fatalf("list failed: %d", code)
	}
	if !strings.Contains(out, "mini") || !strings.Contains(out, "api.minimaxi.com") {
		t.Errorf("list output missing profile: %s", out)
	}

	// use writes the env into settings.json and prints the restart reminder.
	out, code = runModel(t, "use", "mini")
	if code != 0 {
		t.Fatalf("use failed: %d", code)
	}
	if !strings.Contains(out, "Switched to profile mini") || !strings.Contains(out, "--resume") {
		t.Errorf("use output missing confirmation/restart hint: %s", out)
	}
	env := settingsEnvBaseURL()
	if env != "https://api.minimaxi.com/anthropic" {
		t.Errorf("settings env base URL = %q", env)
	}

	// use again is a no-op.
	if _, code := runModel(t, "use", "mini"); code != 0 {
		t.Fatalf("idempotent use failed: %d", code)
	}

	// rm leaves settings.json untouched.
	if _, code := runModel(t, "rm", "mini"); code != 0 {
		t.Fatalf("rm failed: %d", code)
	}
	if got := settingsEnvBaseURL(); got != "https://api.minimaxi.com/anthropic" {
		t.Errorf("settings env must survive rm, got %q", got)
	}
}

func TestModelUseUnknownProfile(t *testing.T) {
	modelEnv(t)
	out, code := runModel(t, "use", "nope")
	if code == 0 || !strings.Contains(out, "no profile named") {
		t.Errorf("expected error for unknown profile, got %d %q", code, out)
	}
}

func TestModelUseProjectScope(t *testing.T) {
	root := modelEnv(t)
	proj := filepath.Join(root, "projA")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := profiles.Load()
	if err != nil {
		t.Fatal(err)
	}
	store.Profiles["claude"] = profiles.Profile{"ANTHROPIC_BASE_URL": "https://api.anthropic.com", "ANTHROPIC_MODEL": "sonnet"}
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}

	if _, code := runModel(t, "use", "claude", "--scope=project", "--project-dir="+proj); code != 0 {
		t.Fatalf("project-scope use failed: %d", code)
	}
	// Project settings got the env; user settings untouched (empty).
	projSettings, err := os.ReadFile(filepath.Join(proj, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(projSettings), "api.anthropic.com") {
		t.Errorf("project settings should carry the env, got %s", projSettings)
	}
	if got := settingsEnvBaseURL(); got != "" {
		t.Errorf("user scope must stay empty, got %q", got)
	}
}

func TestModelListMarksActive(t *testing.T) {
	modelEnv(t)
	store, err := profiles.Load()
	if err != nil {
		t.Fatal(err)
	}
	store.Profiles["mini"] = profiles.Profile{"ANTHROPIC_BASE_URL": "https://api.minimaxi.com/anthropic", "ANTHROPIC_MODEL": "MiniMax-M3"}
	store.Profiles["claude"] = profiles.Profile{"ANTHROPIC_BASE_URL": "https://api.anthropic.com", "ANTHROPIC_MODEL": "sonnet"}
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	// Point settings env at claude.
	if _, code := runModel(t, "use", "claude"); code != 0 {
		t.Fatal(code)
	}
	out, _ := runModel(t, "list")
	if !strings.Contains(out, "* active") || !strings.Contains(out, "claude") {
		t.Errorf("list should mark claude active, got %s", out)
	}
}

func TestModelAddRejectsDuplicate(t *testing.T) {
	modelEnv(t)
	if _, code := runModel(t, "add", "--name", "x", "--base-url", "https://x", "--token", "t", "--model", "m"); code != 0 {
		t.Fatal(code)
	}
	out, code := runModel(t, "add", "--name", "x", "--base-url", "https://y", "--token", "t2", "--model", "m2")
	if code == 0 || !strings.Contains(out, "already exists") {
		t.Errorf("duplicate add must fail, got %d %q", code, out)
	}
}

func TestCheckProfilesReports(t *testing.T) {
	modelEnv(t)
	rp := &report{}
	checkProfiles(rp)
	if len(rp.results) == 0 || rp.results[0].label != "provider profiles" {
		t.Fatalf("expected provider profiles check, got %+v", rp.results)
	}
	// Corrupt the store and expect a warning, not a panic.
	store, _ := profiles.Load()
	if err := os.MkdirAll(filepath.Dir(store.Path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.Path, []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	rp2 := &report{}
	checkProfiles(rp2)
	if rp2.results[0].status != statusWarn {
		t.Errorf("corrupt store should warn, got %+v", rp2.results[0])
	}
}

func TestHostOf(t *testing.T) {
	cases := map[string]string{
		"https://api.minimaxi.com/anthropic": "api.minimaxi.com",
		"http://127.0.0.1:8080":              "127.0.0.1:8080",
		"":                                   "",
	}
	for in, want := range cases {
		if got := hostOf(in); got != want {
			t.Errorf("hostOf(%q) = %q, want %q", in, got, want)
		}
	}
}

// jsonMap is a helper proving settings.json stays valid JSON after model use.
func TestSettingsJSONValidAfterUse(t *testing.T) {
	modelEnv(t)
	if _, code := runModel(t, "add", "--name", "mini", "--base-url", "https://api.minimaxi.com/anthropic", "--token", "sk-t", "--model", "M3"); code != 0 {
		t.Fatal(code)
	}
	if _, code := runModel(t, "use", "mini"); code != 0 {
		t.Fatal(code)
	}
	home, _ := os.UserHomeDir()
	raw, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatalf("settings.json invalid after use: %v\n%s", err, raw)
	}
}
