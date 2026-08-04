package hooks

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zealot00/claude-toolkit/internal/hudstate"
	"github.com/zealot00/claude-toolkit/internal/payload"
	"github.com/zealot00/claude-toolkit/pkg/dir"
)

// writeTranscript is the test helper for laying down a JSONL transcript at
// a known path. Lines shorter than the test wants (e.g. zero lines) is
// handled by the caller via the variadic; empty call = empty file.
func writeTranscript(t *testing.T, lines ...string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")
	if len(lines) == 0 {
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
		return path
	}
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l)
		b.WriteString("\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// stopEvent is the minimum payload for a Stop handler test. The transcript
// path is the only field retryguard reads.
func stopEvent(transcriptPath, permMode string) *payload.Event {
	return &payload.Event{
		HookEventName:  payload.EventStop,
		TranscriptPath: transcriptPath,
		PermissionMode: permMode,
	}
}

func TestRetryGuard_429AndAutoMode_BlocksAndSetsHOOK(t *testing.T) {
	withIsolatedHome(t)
	path := writeTranscript(t,
		`{"type":"assistant","message":{"role":"assistant","content":"thinking..."}}`,
		`{"type":"user","message":{"role":"user","content":"continue"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":"more thinking","stop_reason":"error"},"error":{"type":"rate_limit_error","message":"too many requests"}}`,
	)
	resp, err := retryGuard(context.Background(), stopEvent(path, "bypassPermissions"))
	if err != nil {
		t.Fatalf("retryGuard: %v", err)
	}
	if resp == nil {
		t.Fatal("expected block response, got nil")
	}
	if resp.Decision != "block" {
		t.Errorf("Decision = %q, want %q", resp.Decision, "block")
	}
	if resp.Reason == "" {
		t.Error("Reason should be non-empty")
	}
	st, err := hudstate.Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.Retry != hudstate.RetryHook {
		t.Errorf("hudstate.Retry = %q, want %q", st.Retry, hudstate.RetryHook)
	}
}

func TestRetryGuard_429ButNotAutoMode_DoesNotBlock(t *testing.T) {
	withIsolatedHome(t)
	path := writeTranscript(t,
		`{"type":"assistant","message":{"error":{"type":"rate_limit_error"}}}`,
	)
	for _, mode := range []string{"default", "acceptEdits", "plan"} {
		resp, err := retryGuard(context.Background(), stopEvent(path, mode))
		if err != nil {
			t.Fatalf("retryGuard %s: %v", mode, err)
		}
		if resp != nil {
			t.Errorf("mode %s: expected nil response (user is in control), got %+v", mode, resp)
		}
	}
	// Hudstate SHOULD still be set to HOOK on a hit even when we don't
	// block -- the operator wants to see *why* the previous turn failed.
	st, err := hudstate.Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.Retry != hudstate.RetryHook {
		t.Errorf("hudstate.Retry = %q, want %q", st.Retry, hudstate.RetryHook)
	}
}

func TestRetryGuard_No429_NoBlockAndNoHUDChange(t *testing.T) {
	withIsolatedHome(t)
	// Pre-seed hudstate with PROXY (autoproxy alive) to confirm the no-429
	// path leaves it alone.
	if err := hudstate.Save(hudstate.State{
		Proxy: &hudstate.ProxyState{Port: 8080, Upstream: "https://api.anthropic.com"},
		Retry: hudstate.RetryProxy,
	}); err != nil {
		t.Fatal(err)
	}
	path := writeTranscript(t,
		`{"type":"user","message":{"role":"user","content":"hi"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":"hello"}}`,
	)
	resp, err := retryGuard(context.Background(), stopEvent(path, "bypassPermissions"))
	if err != nil {
		t.Fatalf("retryGuard: %v", err)
	}
	if resp != nil {
		t.Fatalf("expected nil response, got %+v", resp)
	}
	st, err := hudstate.Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.Retry != hudstate.RetryProxy {
		t.Errorf("hudstate.Retry = %q, want preserved %q", st.Retry, hudstate.RetryProxy)
	}
	if st.Proxy == nil {
		t.Error("hudstate.Proxy should be preserved when retryguard finds no 429")
	}
}

func TestRetryGuard_MissingTranscript_FailsOpen(t *testing.T) {
	withIsolatedHome(t)
	resp, err := retryGuard(context.Background(), stopEvent("/no/such/path/transcript.jsonl", "bypassPermissions"))
	if err != nil {
		t.Fatalf("retryGuard missing transcript: %v", err)
	}
	if resp != nil {
		t.Fatalf("missing transcript must fail open, got %+v", resp)
	}
	st, err := hudstate.Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.Retry != "" {
		t.Errorf("hudstate.Retry should be empty (no 429 evidence), got %q", st.Retry)
	}
}

func TestRetryGuard_CorruptJSONL_FailsOpen(t *testing.T) {
	withIsolatedHome(t)
	path := writeTranscript(t,
		`{not valid json`,
		`{"type":"assistant","message":{"error":{"type":"rate_limit_error"}}}`,
		`<<<garbage>>>`,
	)
	// The 429 marker still appears in raw text even with the surrounding
	// JSON being invalid, so the substring scan will find it -- which is
	// the correct behaviour: the transcript is best-effort evidence, not
	// a guaranteed schema.
	resp, err := retryGuard(context.Background(), stopEvent(path, "bypassPermissions"))
	if err != nil {
		t.Fatalf("retryGuard corrupt: %v", err)
	}
	if resp == nil || resp.Decision != "block" {
		t.Fatalf("substring match must still catch 429 in corrupt lines, got %+v", resp)
	}
}

func TestRetryGuard_AllTextLineAtPositionN_StillDetected(t *testing.T) {
	// 429 evidence can be far back in a long transcript. The ring buffer
	// keeps the tail; an old line that scrolled out of the window is
	// still detectable if it falls within transcriptTailLines of the
	// file's end. Cover the boundary: marker at the very last slot.
	withIsolatedHome(t)
	var lines []string
	for range transcriptTailLines - 1 {
		lines = append(lines, `{"type":"assistant","message":{"content":"filler"}}`)
	}
	lines = append(lines, `{"type":"assistant","message":{"error":{"type":"rate_limit_error"}}}`)
	path := writeTranscript(t, lines...)

	resp, err := retryGuard(context.Background(), stopEvent(path, "bypassPermissions"))
	if err != nil {
		t.Fatalf("retryGuard: %v", err)
	}
	if resp == nil || resp.Decision != "block" {
		t.Fatalf("marker at the end of the window must still block, got %+v", resp)
	}
}

func TestRetryGuard_Old429ScrolledOut_NotDetected(t *testing.T) {
	// The opposite boundary: a 429 marker more than transcriptTailLines
	// back must NOT trigger the block, because by now the rate limit has
	// (very probably) cleared and any further wait is just unhelpful.
	withIsolatedHome(t)
	var lines []string
	lines = append(lines, `{"type":"assistant","message":{"error":{"type":"rate_limit_error"}}}`)
	for range transcriptTailLines + 5 {
		lines = append(lines, `{"type":"assistant","message":{"content":"recent"}}`)
	}
	path := writeTranscript(t, lines...)

	resp, err := retryGuard(context.Background(), stopEvent(path, "bypassPermissions"))
	if err != nil {
		t.Fatalf("retryGuard: %v", err)
	}
	if resp != nil {
		t.Fatalf("marker scrolled out of window must not block, got %+v", resp)
	}
}

func TestRetryGuard_CapabilityDisabled_NoBlock(t *testing.T) {
	withIsolatedHome(t)
	root, err := dir.Root()
	if err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(root, "state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "capabilities.json"),
		[]byte(`{"enabled":{"retryguard":false}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	path := writeTranscript(t,
		`{"type":"assistant","message":{"error":{"type":"rate_limit_error"}}}`,
	)
	resp, err := retryGuard(context.Background(), stopEvent(path, "bypassPermissions"))
	if err != nil {
		t.Fatalf("retryGuard: %v", err)
	}
	if resp != nil {
		t.Fatalf("disabled capability must not block, got %+v", resp)
	}
}
