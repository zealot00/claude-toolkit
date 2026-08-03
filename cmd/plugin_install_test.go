package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindPluginSourceFromExeTree(t *testing.T) {
	// Simulate a checkout: <root>/bin/claude-toolkit with .claude-plugin/
	// next to bin/.
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(filepath.Join(root, ".claude-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".claude-plugin", "plugin.json"), []byte(`{"name":"claude-toolkit"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(binDir, "claude-toolkit")

	old := osExecutable
	osExecutable = func() (string, error) { return exe, nil }
	defer func() { osExecutable = old }()

	got, err := findPluginSource()
	if err != nil {
		t.Fatalf("findPluginSource: %v", err)
	}
	if got != root {
		t.Errorf("source = %q, want %q", got, root)
	}
}

func TestFindPluginSourceNone(t *testing.T) {
	old := osExecutable
	osExecutable = func() (string, error) { return filepath.Join(t.TempDir(), "nowhere", "bin", "tool"), nil }
	defer func() { osExecutable = old }()

	if _, err := findPluginSource(); err == nil {
		t.Error("no .claude-plugin anywhere: expected an error")
	}
}

func TestInstallPluginCopiesTree(t *testing.T) {
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, ".claude-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "commands"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(rel, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(src, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".claude-plugin/plugin.json", `{"name":"claude-toolkit"}`)
	write("commands/toolkit.md", "# toolkit command")

	// Point the exe search at src, then install to a fake HOME.
	oldExe := osExecutable
	osExecutable = func() (string, error) { return filepath.Join(src, "bin", "tool"), nil }
	defer func() { osExecutable = oldExe }()

	oldHome := os.Getenv("HOME")
	t.Setenv("HOME", t.TempDir()) // os.UserHomeDir reads $HOME on POSIX

	dst, err := installPlugin(false)
	if err != nil {
		t.Fatalf("installPlugin: %v", err)
	}
	for _, rel := range []string{".claude-plugin/plugin.json", "commands/toolkit.md"} {
		if _, err := os.Stat(filepath.Join(dst, rel)); err != nil {
			t.Errorf("missing copied file %s: %v", rel, err)
		}
	}
	_ = oldHome
}
