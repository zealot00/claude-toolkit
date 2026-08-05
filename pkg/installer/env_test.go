package installer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSettings(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readEnv(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	root := map[string]any{}
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	if raw, ok := root["env"].(map[string]any); ok {
		for k, v := range raw {
			out[k], _ = v.(string)
		}
	}
	return out
}

func TestApplyEnvSetsAndClearsManagedKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	writeSettings(t, path, `{
  "env": {
    "ANTHROPIC_BASE_URL": "https://old.example",
    "ANTHROPIC_AUTH_TOKEN": "sk-old",
    "ANTHROPIC_MODEL": "old-model",
    "API_TIMEOUT_MS": "3000000"
  },
  "enabledPlugins": {"gopls-lsp@claude-plugins-official": true}
}`)

	managed := []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_MODEL"}
	set := map[string]string{
		"ANTHROPIC_BASE_URL": "https://api.minimaxi.com/anthropic",
		"ANTHROPIC_MODEL":    "MiniMax-M3",
	}
	plan, err := ApplyEnv(path, managed, set)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Changed() {
		t.Fatal("expected a change")
	}
	if plan.BackupPath == "" {
		t.Error("expected a backup path when the file changes")
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}

	env := readEnv(t, path)
	if env["ANTHROPIC_BASE_URL"] != "https://api.minimaxi.com/anthropic" {
		t.Errorf("base URL = %q", env["ANTHROPIC_BASE_URL"])
	}
	if env["ANTHROPIC_MODEL"] != "MiniMax-M3" {
		t.Errorf("model = %q", env["ANTHROPIC_MODEL"])
	}
	if _, stale := env["ANTHROPIC_AUTH_TOKEN"]; stale {
		t.Error("managed key not in set must be removed (stale token!)")
	}
	if env["API_TIMEOUT_MS"] != "3000000" {
		t.Error("unmanaged key must be preserved")
	}
}

func TestApplyEnvNoopWhenUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	writeSettings(t, path, `{
  "env": {
    "ANTHROPIC_BASE_URL": "https://api.minimaxi.com/anthropic"
  }
}
`)
	plan, err := ApplyEnv(path, []string{"ANTHROPIC_BASE_URL"}, map[string]string{"ANTHROPIC_BASE_URL": "https://api.minimaxi.com/anthropic"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Changed() {
		t.Error("identical env must produce a no-op plan")
	}
}

func TestApplyEnvClearingAllEnvRemovesKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	writeSettings(t, path, `{"env":{"ANTHROPIC_BASE_URL":"https://x"},"model":"MiniMax-M3"}`)
	plan, err := ApplyEnv(path, []string{"ANTHROPIC_BASE_URL"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Changed() {
		t.Fatal("expected change")
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), `"env"`) {
		t.Errorf("empty env block should be removed, got %s", data)
	}
	if !strings.Contains(string(data), `"model"`) {
		t.Error("other top-level keys must survive")
	}
}
