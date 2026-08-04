// Package hudstate persists the fields that the HUD command renders in the
// Claude Code status line.
//
// Why a separate file: the HUD shows a live view of multiple subsystems
// (the autoproxy child process, the retryguard fallback state, the active
// permission mode). Each subsystem owns writing its own slice of the state;
// the HUD command only reads. Keeping the schema here -- in one tiny
// package with no hook dependencies -- makes that write/read split obvious
// and prevents any subsystem from accidentally corrupting the others.
//
// State file: <root>/state/hud.json (root resolved by pkg/dir). Writes are
// atomic so a half-written file cannot wedge the HUD command. Reads of a
// missing or corrupt file return the zero State, so the HUD falls through
// to "no info" rather than going blank.
package hudstate

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/zealot00/claude-toolkit/pkg/dir"
)

// Retry values are the three observable states of the toolkit's 429
// handling: HOOK means retryguard is the active path, PROXY means autoproxy
// has a live child process, OFF means neither is engaged.
const (
	RetryHook  = "HOOK"
	RetryProxy = "PROXY"
	RetryOff   = "OFF"
)

// ProxyState describes a live autoproxy child process.
type ProxyState struct {
	Port     int    `json:"port"`
	Upstream string `json:"upstream"`
}

// State is the HUD's snapshot of toolkit subsystem status.
type State struct {
	UpdatedAt time.Time   `json:"updated_at"`
	Proxy     *ProxyState `json:"proxy,omitempty"`
	Retry     string      `json:"retry"`
	Mode      string      `json:"mode,omitempty"`
}

// path returns the on-disk location of hud.json, creating the parent
// directory (0700) on the way. Errors from the parent dir propagate; this
// is the only place the HUD's state dir layout is decided.
func path() (string, error) {
	root, err := dir.Root()
	if err != nil {
		return "", err
	}
	state := filepath.Join(root, "state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(state, "hud.json"), nil
}

// Load reads the persisted state. A missing or corrupt file returns the
// zero State and a nil error -- the HUD must still render its other
// fields, and there is no recoverable failure here that warrants a wedge.
func Load() (State, error) {
	p, err := path()
	if err != nil {
		return State{}, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return State{}, nil
		}
		return State{}, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}, nil // corrupt -> zero state, no error
	}
	return s, nil
}

// Save writes the state atomically (write to a unique .tmp file, close,
// rename over the target). A nil error means the file is durably on disk.
// UpdatedAt is stamped by Save so callers do not need to remember.
//
// Save is safe to call concurrently: each call gets its own tmp file via
// os.CreateTemp, so the only race is the final os.Rename -- and rename is
// atomic on POSIX, so the LAST writer wins on contents and no call can
// leave a half-written target behind.
func Save(s State) error {
	p, err := path()
	if err != nil {
		return err
	}
	s.UpdatedAt = time.Now().UTC()
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), "hud-*.json.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, p); err != nil {
		cleanup()
		return err
	}
	return nil
}
