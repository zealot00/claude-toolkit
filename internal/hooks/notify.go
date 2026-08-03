package hooks

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/zealot00/claude-toolkit/internal/dispatcher"
	"github.com/zealot00/claude-toolkit/internal/payload"
	"github.com/zealot00/claude-toolkit/pkg/dir"
)

// Notifier is the "notify" capability. It records when a tool call starts
// (PreToolUse) and, on PostToolUse, fires an OS desktop notification plus a
// terminal bell when the call ran longer than the configured threshold OR
// failed outright.
//
// It is OFF by default: set CLAUDE_TOOLKIT_NOTIFY=<seconds> to enable. Hook
// processes are separate invocations, so the start timestamp is persisted in
// the toolkit's private state dir (0700) rather than held in memory.
func Notifier() *dispatcher.Route {
	return &dispatcher.Route{
		Name:    "notify",
		Events:  []string{payload.EventPreToolUse, payload.EventPostToolUse},
		Handler: notifier,
	}
}

// notifyThreshold reads CLAUDE_TOOLKIT_NOTIFY (seconds). 0/absent = disabled.
func notifyThreshold() (time.Duration, bool) {
	v := os.Getenv("CLAUDE_TOOLKIT_NOTIFY")
	if v == "" {
		return 0, false
	}
	sec, err := strconv.Atoi(v)
	if err != nil || sec <= 0 {
		return 0, false
	}
	return time.Duration(sec) * time.Second, true
}

// notifySend is indirected so tests can stub the desktop-notification path.
var notifySend = func(title, message string) {
	msg := message
	switch runtime.GOOS {
	case "darwin":
		// osascript argument is a single -e; the message must survive quoting.
		script := fmt.Sprintf("display notification %s with title %s", quoteApple(msg), quoteApple(title))
		_ = exec.Command("osascript", "-e", script).Run()
	case "linux":
		_ = exec.Command("notify-send", title, msg).Run()
	}
	// Terminal bell, portable everywhere.
	fmt.Print("\a")
}

func quoteApple(s string) string {
	return `"` + s + `"`
}

func notifier(_ context.Context, e *payload.Event) (*payload.Response, error) {
	threshold, enabled := notifyThreshold()
	if !enabled {
		return nil, nil
	}
	if e.ToolName == "" {
		return nil, nil
	}

	path, err := notifyStatePath()
	if err != nil {
		return nil, nil // fail open
	}
	key := notifyKey(e)

	switch e.HookEventName {
	case payload.EventPreToolUse:
		st := loadNotifyState(path)
		st.Timestamps[key] = time.Now().UnixMilli()
		saveNotifyState(path, st)
	case payload.EventPostToolUse:
		st := loadNotifyState(path)
		start, ok := st.Timestamps[key]
		delete(st.Timestamps, key)
		if !ok {
			return nil, nil // no matching PreToolUse (session resumed mid-way)
		}
		elapsed := time.Duration(time.Now().UnixMilli()-start) * time.Millisecond

		failed := false
		if exit, ok := parseExitCode(e.ToolResponse); ok && exit != 0 {
			failed = true
		}
		if elapsed < threshold && !failed {
			return nil, nil
		}
		verb := "finished"
		if failed {
			verb = "FAILED"
		}
		notifySend("claude-toolkit", fmt.Sprintf("%s %s in %s", e.ToolName, verb, elapsed.Round(time.Second)))
	}
	return nil, nil
}

// notifyKey is a stable per-invocation key: cwd + tool + hash of tool_input.
// The input is hashed so command text never lands in the state file.
func notifyKey(e *payload.Event) string {
	h := sha256.Sum256(e.ToolInput)
	return e.Cwd + "|" + e.ToolName + "|" + hex.EncodeToString(h[:8])
}

// notifyState is the persisted start-timestamp ledger.
type notifyState struct {
	Timestamps map[string]int64 `json:"timestamps"`
}

func notifyStatePath() (string, error) {
	root, err := dir.Root()
	if err != nil {
		return "", err
	}
	state := filepath.Join(root, "state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(state, "timestamps.json"), nil
}

func loadNotifyState(path string) notifyState {
	st := notifyState{Timestamps: map[string]int64{}}
	data, err := os.ReadFile(path)
	if err != nil {
		return st
	}
	_ = json.Unmarshal(data, &st)
	if st.Timestamps == nil {
		st.Timestamps = map[string]int64{}
	}
	return st
}

func saveNotifyState(path string, st notifyState) {
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
