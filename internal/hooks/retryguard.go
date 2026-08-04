package hooks

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"strings"

	"github.com/zealot00/claude-toolkit/internal/capcfg"
	"github.com/zealot00/claude-toolkit/internal/dispatcher"
	"github.com/zealot00/claude-toolkit/internal/hudstate"
	"github.com/zealot00/claude-toolkit/internal/payload"
)

// RetryGuard is the "retryguard" capability. It is the fallback path when
// the user has not opted into a network proxy and autoproxy therefore did
// not fork a 429 retry proxy.
//
// Claude Code's internal retry loop will already have given up by the time
// the Stop event fires, so all retryguard can do is nudge Claude to wait
// before trying again: it scans the tail of the session transcript for any
// 429 / rate-limit marker, and on a hit (in auto mode) returns a block
// decision with a reason that becomes a user-facing message injected into
// the next turn.
//
// Why the Stop hook and not PreToolUse: a PreToolUse hook can only inspect
// the upcoming tool call; it cannot tell whether the previous request
// failed because of rate limiting. The transcript is the only persistent
// record, and reading it on Stop gives us the post-mortem view we need.
//
// Failure modes are fail-open: a missing or unreadable transcript produces
// no opinion, not a spurious block. The hook must never be the reason a
// session ends.
func RetryGuard() *dispatcher.Route {
	return &dispatcher.Route{
		Name:    "retryguard",
		Events:  []string{payload.EventStop},
		Handler: retryGuard,
	}
}

// transcriptTailLines is the window we scan for 429 evidence. 50 lines is
// enough to cover several back-and-forth turns without forcing the scan to
// walk through megabytes of older history; sessions long enough for the
// rolling window to be stale are also long enough that a few extra seconds
// of backoff makes no difference.
const transcriptTailLines = 50

// rateLimitMarkers are the substring patterns that count as 429 evidence.
// Centralised here so a Claude Code schema change can be absorbed with a
// single edit and a corresponding test update -- rather than scattered
// string checks throughout the codebase.
var rateLimitMarkers = []string{
	`"rate_limit_error"`,
	`"too_many_requests"`,
	`"Rate limit"`,
	`"rate limit"`,
	`"Too Many Requests"`,
	`"too many requests"`,
}

// transcriptHas429 scans the last transcriptTailLines of path for any of
// rateLimitMarkers. A missing or unreadable file is reported as a non-hit
// (with err set) so the caller can fail open.
func transcriptHas429(path string) (hit bool, err error) {
	if path == "" {
		return false, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()

	// Bounded ring buffer: keep the most recent N lines without holding
	// the whole transcript in memory. A single line is unbounded in
	// principle, but Claude Code transcripts are line-delimited JSON and
	// bufio.Scanner's default 64 KiB cap is plenty for any one entry.
	scanner := bufio.NewScanner(f)
	tail := make([][]byte, 0, transcriptTailLines)
	for scanner.Scan() {
		if len(tail) == transcriptTailLines {
			tail = tail[1:]
		}
		tail = append(tail, append([]byte(nil), scanner.Bytes()...))
	}
	if err := scanner.Err(); err != nil {
		return false, err
	}

	for _, line := range tail {
		if containsAny(line, rateLimitMarkers) {
			return true, nil
		}
	}
	return false, nil
}

// containsAny reports whether any of needles appears as a substring of hay.
func containsAny(hay []byte, needles []string) bool {
	for _, n := range needles {
		if bytes.Contains(hay, []byte(n)) {
			return true
		}
	}
	return false
}

func retryGuard(_ context.Context, e *payload.Event) (*payload.Response, error) {
	disabled, err := capcfg.Disabled()
	if err != nil {
		return nil, nil // fail open
	}
	if disabled["retryguard"] {
		return nil, nil
	}

	hit, _ := transcriptHas429(e.TranscriptPath)
	if !hit {
		return nil, nil // no opinion; let the session end
	}

	// Update the HUD so the operator can see the fallback engaged. We
	// never clear Retry here: autoproxy owns the OFF transition on
	// SessionEnd, and clearing on every clean Stop would race with the
	// PROXY state autoproxy writes on its own SessionStart.
	if err := hudstate.Save(hudstate.State{Retry: hudstate.RetryHook}); err != nil {
		// hudstate is observability; the block below is the real verdict.
	}

	if e.PermissionMode != "bypassPermissions" {
		return nil, nil
	}
	return payload.Block(strings.TrimSpace(`
claude-toolkit retryguard: previous turn ended on a 429 / rate limit and Claude Code's internal retry gave up. The upstream API has not recovered. Waiting briefly before the next request is the safe move.

`)), nil
}
