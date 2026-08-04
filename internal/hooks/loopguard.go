package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zealot00/claude-toolkit/internal/dispatcher"
	"github.com/zealot00/claude-toolkit/internal/payload"
	"github.com/zealot00/claude-toolkit/pkg/dir"
)

// LoopGuard is the "loopguard" capability: it detects when the exact same
// Bash command keeps failing and blocks it before Claude burns another turn on
// a doomed retry.
//
// Failure can only be known after a command runs, so this route listens on
// both sides: PostToolUse records an exit status when the response carries one
// (exit_code / exitCode / interrupted), PostToolUseFailure records the
// failure the official way (it is the event Claude Code fires for non-zero
// exits), and PreToolUse looks the command up before it runs. A command that
// has failed three times in a row is denied with a diagnostic; any successful
// run (or a different command) resets the count.
//
// The state file lives in the toolkit's private state dir (0700), not /tmp,
// which is world-writable and shared across users.
func LoopGuard() *dispatcher.Route {
	return &dispatcher.Route{
		Name:    "loopguard",
		Events:  []string{payload.EventPreToolUse, payload.EventPostToolUse, payload.EventPostToolUseFailure},
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
		logFirstBashResponse(e.ToolResponse)
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

	case payload.EventPostToolUseFailure:
		// The official failure signal: no exit code parsing needed, the event
		// itself means the command did not succeed.
		st := loadLoopState(path)
		entry := st.Commands[in.Command]
		entry.Fails++
		entry.Exit = 1
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
	for _, key := range []string{"exit_code", "exitCode", "exitcode", "ExitCode"} {
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

// logFirstBashResponse dumps the raw tool_response JSON from the first Bash
// PostToolUse event we see into ~/.claude-toolkit/log/bash-response-fields.json,
// then marks a sentinel so subsequent calls are no-ops. This exists because
// Claude Code's actual field name for the exit status (exit_code? exitCode?
// interrupted?) drifts across versions and guessing wrong silently breaks
// loopguard. Capturing one real payload settles the question.
//
// Failures here never propagate — this is best-effort telemetry; a write error
// must not turn a passing hook into a failing one.
func logFirstBashResponse(toolResponse json.RawMessage) {
	root, err := dir.Root()
	if err != nil {
		return
	}
	logDir := filepath.Join(root, "log")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return
	}
	sentinel := filepath.Join(logDir, "bash-response-fields.json.sampled")
	if _, err := os.Stat(sentinel); err == nil {
		return // already captured once; do not churn the file every Bash call
	}

	keys := map[string]json.RawMessage{}
	if len(toolResponse) > 0 {
		// A non-object tool_response would surprise us; record it as such.
		_ = json.Unmarshal(toolResponse, &keys)
	}
	keyNames := make([]string, 0, len(keys))
	for k := range keys {
		keyNames = append(keyNames, k)
	}
	sort.Strings(keyNames)

	record := struct {
		Timestamp string            `json:"timestamp"`
		Keys      []string          `json:"keys"`
		Raw       json.RawMessage   `json:"raw"`
		KeyTypes  map[string]string `json:"key_types"`
	}{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Keys:      keyNames,
		Raw:       toolResponse,
		KeyTypes:  inferKeyTypes(keys),
	}

	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return
	}
	target := filepath.Join(logDir, "bash-response-fields.json")
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	if err := os.Rename(tmp, target); err != nil {
		return
	}
	// Best-effort sentinel so we do not rewrite on every Bash PostToolUse.
	_ = os.WriteFile(sentinel, []byte(record.Timestamp), 0o600)
}

// inferKeyTypes reports the JSON type of each key's value so a human reading
// the dump can see "this key is a number" vs "this key is a boolean" without
// having to open the raw blob.
func inferKeyTypes(m map[string]json.RawMessage) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = jsonType(v)
	}
	return out
}

// jsonType names the leading JSON token of v. It only needs to distinguish
// the few shapes a Bash tool_response is plausibly made of.
func jsonType(v json.RawMessage) string {
	t := strings.TrimSpace(string(v))
	if t == "" {
		return "empty"
	}
	switch t[0] {
	case '{':
		return "object"
	case '[':
		return "array"
	case '"':
		return "string"
	case 't', 'f':
		return "bool"
	case 'n':
		return "null"
	}
	if (t[0] >= '0' && t[0] <= '9') || t[0] == '-' {
		return "number"
	}
	return "unknown"
}
