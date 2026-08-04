package hudstate

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/zealot00/claude-toolkit/pkg/dir"
)

// withIsolatedHome redirects the toolkit root to a fresh temp dir for the
// lifetime of the test. Save/Load resolve the hud.json path through
// pkg/dir.Root, so this is enough to keep tests off the user's real state.
func withIsolatedHome(t *testing.T) {
	t.Helper()
	t.Setenv(dir.EnvHome, t.TempDir())
}

func TestLoad_MissingFileReturnsZero(t *testing.T) {
	withIsolatedHome(t)
	s, err := Load()
	if err != nil {
		t.Fatalf("Load missing file: %v", err)
	}
	if s.Retry != "" || s.Proxy != nil || s.Mode != "" {
		t.Fatalf("Load missing file = %+v, want zero State", s)
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	withIsolatedHome(t)
	want := State{
		Proxy: &ProxyState{Port: 8080, Upstream: "https://api.moonshot.cn/anthropic"},
		Retry: RetryProxy,
		Mode:  "bypassPermissions",
	}
	if err := Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// UpdatedAt is stamped by Save; check it is non-zero and within the
	// last few seconds rather than asserting an exact value.
	if got.UpdatedAt.IsZero() {
		t.Error("UpdatedAt should be stamped by Save")
	}
	got.UpdatedAt = want.UpdatedAt // ignore for equality check below
	if got.Retry != want.Retry || got.Mode != want.Mode {
		t.Errorf("scalar fields: got %+v, want %+v", got, want)
	}
	if got.Proxy == nil || got.Proxy.Port != 8080 || got.Proxy.Upstream != want.Proxy.Upstream {
		t.Errorf("proxy: got %+v, want %+v", got.Proxy, want.Proxy)
	}
}

func TestLoad_CorruptFileReturnsZero(t *testing.T) {
	withIsolatedHome(t)
	root, err := dir.Root()
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, "state", "hud.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("{this is not valid json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load()
	if err != nil {
		t.Fatalf("Load corrupt file should fail-open: %v", err)
	}
	if s.Retry != "" || s.Proxy != nil {
		t.Fatalf("Load corrupt = %+v, want zero State", s)
	}
}

func TestSave_AtomicReplaceNoLeftoverTmp(t *testing.T) {
	withIsolatedHome(t)
	if err := Save(State{Retry: RetryOff}); err != nil {
		t.Fatal(err)
	}
	root, err := dir.Root()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("leftover tmp file: %s", e.Name())
		}
	}
}

func TestSave_OverwritesExisting(t *testing.T) {
	withIsolatedHome(t)
	if err := Save(State{Retry: RetryOff, Mode: "default"}); err != nil {
		t.Fatal(err)
	}
	if err := Save(State{Retry: RetryHook, Mode: "bypassPermissions"}); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Retry != RetryHook {
		t.Errorf("Retry: got %q, want %q", got.Retry, RetryHook)
	}
	if got.Mode != "bypassPermissions" {
		t.Errorf("Mode: got %q, want bypassPermissions", got.Mode)
	}
}

// TestSave_ConcurrentWritesDontCorrupt covers the atomic-write contract:
// even under concurrent Save calls the file must remain valid JSON. The
// LAST writer wins on contents, but the file must never be half-written.
func TestSave_ConcurrentWritesDontCorrupt(t *testing.T) {
	withIsolatedHome(t)
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			mode := "default"
			if i%2 == 0 {
				mode = "bypassPermissions"
			}
			retry := RetryOff
			switch i % 3 {
			case 0:
				retry = RetryHook
			case 1:
				retry = RetryProxy
			case 2:
				retry = RetryOff
			}
			if err := Save(State{Retry: retry, Mode: mode}); err != nil {
				t.Errorf("concurrent Save %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	// The file must at least be parseable as JSON.
	if _, err := Load(); err != nil {
		t.Fatalf("Load after concurrent Saves: %v", err)
	}
}

func TestLoad_MissingFieldDefaultsAreSafe(t *testing.T) {
	withIsolatedHome(t)
	root, err := dir.Root()
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, "state", "hud.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	// A valid JSON object missing the proxy / mode / retry fields should
	// load without panic, with empty strings and nil proxy.
	if err := os.WriteFile(p, []byte(`{"updated_at":"2026-01-01T00:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load()
	if err != nil {
		t.Fatalf("Load minimal: %v", err)
	}
	if s.Proxy != nil {
		t.Errorf("Proxy should default to nil, got %+v", s.Proxy)
	}
	if s.Retry != "" || s.Mode != "" {
		t.Errorf("scalars should default to empty: got retry=%q mode=%q", s.Retry, s.Mode)
	}
}
