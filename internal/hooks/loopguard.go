package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zealot00/claude-toolkit/internal/dispatcher"
	"github.com/zealot00/claude-toolkit/internal/payload"
	"github.com/zealot00/claude-toolkit/pkg/dir"
)

// LoopGuard is the "loopguard" capability: it detects when the exact same
// Bash command keeps failing and blocks it before Claude burns another turn on
// a doomed retry.
//
// Failure can only be known after a command runs, so this route listens on
// both events: PostToolUse records the exit status into a state file, and
// PreToolUse looks the command up before it runs. A command that has failed
// three times in a row is denied with a diagnostic; any successful run (or a
// different command) resets the count.
//
// The state file lives in the toolkit's private state dir (0700), not /tmp,
// which is world-writable and shared across users.
func LoopGuard() *dispatcher.Route {
	return &dispatcher.Route{
		Name:    "loopguard",
		Events:  []string{payload.EventPreToolUse, payload.EventPostToolUse},
		Tools:   []string{"Bash"},
		Handler: loopGuard,
	}
}

// maxConsecutiveFails is how many identical failing runs earn a denial.
const maxConsecutiveFails = 3

// loopState is the persisted failure ledger. The map key is the command text.
type loopState struct {
	Commands map[string]loopEntry `json:"commands"`
}

type loopEntry struct {
	Fails int `json:"fails"`
	Exit  int `json:"exit"`
}

func loopGuard(_ context.Context, e *payload.Event) (*payload.Response, error) {
	in, err := e.Bash()
	if err != nil || in.Command == "" {
		return nil, nil
	}

	path, err := statePath()
	if err != nil {
		return nil, nil // fail open: no state, no opinion
	}

	switch e.HookEventName {
	case payload.EventPreToolUse:
		st := loadLoopState(path)
		entry, ok := st.Commands[in.Command]
		if !ok || entry.Fails < maxConsecutiveFails {
			return nil, nil
		}
		return payload.Deny(fmt.Sprintf(
			"Blocked by claude-toolkit loopguard:\n  - [bash-loop] this exact command has failed %d consecutive times (last exit %d). Re-running it unchanged will likely fail again; change the command or the underlying state first.",
			entry.Fails, entry.Exit)), nil

	case payload.EventPostToolUse:
		exit, ok := parseExitCode(e.ToolResponse)
		if !ok {
			return nil, nil // could not read the exit status; do not guess
		}
		st := loadLoopState(path)
		entry := st.Commands[in.Command]
		if exit == 0 {
			entry.Fails = 0 // a success clears the streak
		} else {
			entry.Fails++
		}
		entry.Exit = exit
		st.Commands[in.Command] = entry
		saveLoopState(path, st)
		return nil, nil
	}
	return nil, nil
}

// statePath resolves the loop-guard ledger under the toolkit's private state
// directory, creating it with 0700.
func statePath() (string, error) {
	root, err := dir.Root()
	if err != nil {
		return "", err
	}
	state := filepath.Join(root, "state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(state, "bash_history.json"), nil
}

func loadLoopState(path string) loopState {
	st := loopState{Commands: map[string]loopEntry{}}
	data, err := os.ReadFile(path)
	if err != nil {
		return st
	}
	// Ignore any parse error: a corrupt ledger is not worth blocking on.
	_ = json.Unmarshal(data, &st)
	if st.Commands == nil {
		st.Commands = map[string]loopEntry{}
	}
	return st
}

func saveLoopState(path string, st loopState) {
	data, err := json.Marshal(st)
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

// parseExitCode reads the Bash tool's exit status from its tool_response. The
// field appears as exit_code / exitCode depending on the version; interrupted
// counts as a failure (130). ok=false when the status cannot be determined.
func parseExitCode(toolResponse json.RawMessage) (int, bool) {
	if len(toolResponse) == 0 {
		return 0, false
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(toolResponse, &m); err != nil {
		return 0, false
	}
	for _, key := range []string{"exit_code", "exitCode", "exitcode"} {
		if raw, ok := m[key]; ok {
			var n int
			if err := json.Unmarshal(raw, &n); err == nil {
				return n, true
			}
			var f float64
			if err := json.Unmarshal(raw, &f); err == nil {
				return int(f), true
			}
		}
	}
	// A tool call the user or Claude interrupted is a failure, not a pass.
	if raw, ok := m["interrupted"]; ok {
		var b bool
		if err := json.Unmarshal(raw, &b); err == nil && b {
			return 130, true
		}
	}
	return 0, false
}
