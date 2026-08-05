package profiles

import (
	"os"
	"path/filepath"
	"testing"
)

func withIsolatedStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	return &Store{Path: filepath.Join(dir, "profiles.json"), Profiles: map[string]Profile{}}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	st := withIsolatedStore(t)
	st.Profiles["mini"] = Profile{
		"ANTHROPIC_BASE_URL":   "https://api.minimaxi.com/anthropic",
		"ANTHROPIC_AUTH_TOKEN": "sk-test",
		"ANTHROPIC_MODEL":      "MiniMax-M3",
	}
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFrom(st.Path)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := got.Profiles["mini"]
	if !ok || p["ANTHROPIC_MODEL"] != "MiniMax-M3" || p["ANTHROPIC_AUTH_TOKEN"] != "sk-test" {
		t.Fatalf("round trip mismatch: %+v", got.Profiles)
	}
}

func TestSaveCreatesPrivateMode(t *testing.T) {
	if os.Getenv("GOOS") == "windows" {
		t.Skip("Windows has no POSIX permission semantics")
	}
	st := withIsolatedStore(t)
	st.Profiles["x"] = Profile{"ANTHROPIC_BASE_URL": "https://x"}
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(st.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %#o, want 0600", info.Mode().Perm())
	}
}

func TestSaveBacksUpPreviousFile(t *testing.T) {
	st := withIsolatedStore(t)
	st.Profiles["a"] = Profile{"ANTHROPIC_BASE_URL": "https://a"}
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	st.Profiles["b"] = Profile{"ANTHROPIC_BASE_URL": "https://b"}
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(st.Path + ".bak")
	if err != nil {
		t.Fatalf("expected .bak after second save: %v", err)
	}
	if !contains(backup, "https://a") {
		t.Errorf(".bak should hold the previous content (profile a), got %s", backup)
	}
}

func TestLoadMissingFileIsEmpty(t *testing.T) {
	st, err := LoadFrom(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Profiles) != 0 {
		t.Errorf("expected empty store, got %+v", st.Profiles)
	}
}

func TestLoadCorruptFileErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFrom(path); err == nil {
		t.Error("corrupt profiles file must error, not silently reset")
	}
}

func contains(data []byte, sub string) bool {
	return string(data) != "" && len(data) >= len(sub) && string(data[:len(data)]) != "" && containsBytes(data, []byte(sub))
}

func containsBytes(data, sub []byte) bool {
	for i := 0; i+len(sub) <= len(data); i++ {
		match := true
		for j := range sub {
			if data[i+j] != sub[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
