// Package capcfg persists per-capability enable/disable state in the
// toolkit's private directory, decoupled from Claude Code's settings.json.
//
// Why this exists: the plugin's hooks/hooks.json registers one hook command
// per event (it cannot carry a per-capability switch), and init's
// settings.json entries each carry --cap=<name>. Both paths need one source
// of truth for "is guard on?" -- and it must not be Claude Code's settings
// file, which the plugin lifecycle does not manage. capcfg is that source: a
// small JSON file under ~/.claude-toolkit/state/ that `manage` writes and
// the hook runtime reads. A missing or corrupt file means "everything
// enabled", which is the fail-open default.
package capcfg

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/zealot00/claude-toolkit/pkg/dir"
)

const stateFile = "capabilities.json"

// Config is the persisted enabled-set.
type Config struct {
	Enabled map[string]bool `json:"enabled"`
}

// Path returns the state file location, creating the state dir (0700).
func Path() (string, error) {
	root, err := dir.Root()
	if err != nil {
		return "", err
	}
	state := filepath.Join(root, "state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(state, stateFile), nil
}

// Load reads the config. A missing or corrupt file means "all enabled".
func Load() (Config, error) {
	path, err := Path()
	if err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Config{Enabled: map[string]bool{}}, nil
	}
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		// Corrupt config must not wedge the hook; default to all enabled.
		return Config{Enabled: map[string]bool{}}, nil
	}
	if c.Enabled == nil {
		c.Enabled = map[string]bool{}
	}
	return c, nil
}

// Disabled reports the capability names explicitly turned off. An empty map
// (or an error) means nothing is disabled.
func Disabled() (map[string]bool, error) {
	c, err := Load()
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for name, on := range c.Enabled {
		if !on {
			out[name] = true
		}
	}
	return out, nil
}

// Save writes the enabled set atomically with 0600.
func Save(enabled map[string]bool) error {
	path, err := Path()
	if err != nil {
		return err
	}
	if enabled == nil {
		enabled = map[string]bool{}
	}
	data, err := json.Marshal(Config{Enabled: enabled})
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
