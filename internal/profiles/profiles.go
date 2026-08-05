// Package profiles persists the toolkit's provider profiles. A profile is a
// set of ANTHROPIC_* env overrides (base URL, auth token, model selection)
// that `claude-toolkit model use` copies into the settings.json env block.
//
// Storage: <toolkit root>/profiles.json. The root follows pkg/dir:
// $CLAUDE_TOOLKIT_HOME, $CLAUDE_PLUGIN_DATA,
// ~/.claude/plugins/data/claude-toolkit/, then ~/.claude-toolkit/ (legacy,
// auto-migrated). Because Claude Code deletes the plugin-data directory with
// the plugin lifecycle, `/plugin uninstall` (and `uninstall --purge-config`)
// remove profiles too — the README says so explicitly.
//
// Reliability: the file is created 0600 (it holds API tokens), every write
// is atomic (temp file + fsync + rename, so a crash cannot leave a truncated
// file), and the previous file is copied to profiles.json.bak before the
// first write so a bad edit can be recovered by hand.
package profiles

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/zealot00/claude-toolkit/pkg/dir"
)

// Profile is one provider configuration: a set of ANTHROPIC_* env keys that
// will be merged into the settings.json env block on `model use`.
type Profile map[string]string

// ManagedKeys are the env keys a profile may set and that `model use` clears
// from settings.json before applying a profile (so switching providers never
// leaves a stale token or model behind).
var ManagedKeys = []string{
	"ANTHROPIC_BASE_URL",
	"ANTHROPIC_AUTH_TOKEN",
	"ANTHROPIC_API_KEY",
	"ANTHROPIC_MODEL",
	"ANTHROPIC_DEFAULT_SONNET_MODEL",
	"ANTHROPIC_DEFAULT_OPUS_MODEL",
	"ANTHROPIC_DEFAULT_HAIKU_MODEL",
}

// Store is the on-disk profiles file.
type Store struct {
	Path     string
	Profiles map[string]Profile
}

// Path returns the canonical profiles file location.
func Path() (string, error) {
	root, err := dir.Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "profiles.json"), nil
}

// Load reads the store. A missing file yields an empty store, never an error.
func Load() (*Store, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}
	return LoadFrom(p)
}

// LoadFrom reads the store at an explicit path (used by tests).
func LoadFrom(path string) (*Store, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Store{Path: path, Profiles: map[string]Profile{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("profiles: read %s: %w", path, err)
	}
	m := map[string]Profile{}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("profiles: %s is not valid JSON: %w", path, err)
	}
	if m == nil {
		m = map[string]Profile{}
	}
	return &Store{Path: path, Profiles: m}, nil
}

// Save writes the store atomically (0600), backing up the previous file to
// profiles.json.bak first so a bad edit is recoverable by hand.
func (s *Store) Save() error {
	data, err := json.MarshalIndent(s.Profiles, "", "  ")
	if err != nil {
		return fmt.Errorf("profiles: encode: %w", err)
	}
	data = append(data, '\n')

	dirPath := filepath.Dir(s.Path)
	if err := os.MkdirAll(dirPath, 0o700); err != nil {
		return fmt.Errorf("profiles: create %s: %w", dirPath, err)
	}
	if _, err := os.Stat(s.Path); err == nil {
		if err := copyFile(s.Path, s.Path+".bak"); err != nil {
			// Backup is best-effort; the atomic write below is the real
			// protection. Surface it so users see recovery is unavailable.
			fmt.Fprintf(os.Stderr, "note: profiles backup failed: %v\n", err)
		}
	}
	return atomicWrite(s.Path, data, 0o600)
}

// copyFile copies src to dst preserving content (used for the .bak backup).
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// atomicWrite writes data to path via a temp file + fsync + rename, so a
// crash cannot leave path pointing at a truncated inode.
func atomicWrite(path string, data []byte, mode os.FileMode) error {
	d := filepath.Dir(path)
	f, err := os.CreateTemp(d, ".profiles-*.tmp")
	if err != nil {
		return fmt.Errorf("profiles: create temp in %s: %w", d, err)
	}
	tmp := f.Name()
	defer os.Remove(tmp)

	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("profiles: write temp: %w", err)
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return fmt.Errorf("profiles: chmod temp: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("profiles: sync temp: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("profiles: close temp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("profiles: rename into %s: %w", path, err)
	}
	return nil
}
