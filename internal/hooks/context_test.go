package hooks

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zealot00/claude-toolkit/internal/payload"
)

func TestTruncateContext(t *testing.T) {
	short := strings.Repeat("a", 100)
	if got := truncateContext(short, maxContextLen); got != short {
		t.Error("short input must pass through unchanged")
	}

	long := strings.Repeat("b", 12000)
	got := truncateContext(long, maxContextLen)
	const marker = "\n...(truncated by claude-toolkit)"
	if len(got) != maxContextLen+len(marker) {
		t.Fatalf("truncated length = %d, want %d", len(got), maxContextLen+len(marker))
	}
	if !strings.HasSuffix(got, marker) {
		t.Errorf("truncation marker missing: tail %q", got[len(got)-40:])
	}
	if !strings.HasPrefix(got, strings.Repeat("b", maxContextLen)) {
		t.Error("truncated content should keep the head of the input")
	}
}

// TestTruncateAtExactLimit: content at the limit must not gain a marker.
func TestTruncateAtExactLimit(t *testing.T) {
	s := strings.Repeat("c", maxContextLen)
	if got := truncateContext(s, maxContextLen); got != s {
		t.Error("content at the limit should pass through unchanged")
	}
}

// TestUserPromptSubmitInjectsOneLine: the re-injection on every prompt must
// stay tiny, so a long session notices cwd drift without burning tokens.
func TestUserPromptSubmitInjectsOneLine(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := &payload.Event{
		HookEventName: payload.EventUserPromptSubmit,
		Cwd:           dir,
	}
	resp, err := sessionContext(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil || resp.HookSpecificOutput == nil || resp.HookSpecificOutput.AdditionalContext == "" {
		t.Fatal("UserPromptSubmit should inject a cwd line")
	}
	if !strings.Contains(resp.HookSpecificOutput.AdditionalContext, "cwd: "+dir) {
		t.Errorf("missing cwd in %q", resp.HookSpecificOutput.AdditionalContext)
	}
	// Not a git repo, so no dirty count is appended; the line must be short.
	if len(resp.HookSpecificOutput.AdditionalContext) > 200 {
		t.Errorf("prompt re-injection too long: %d chars", len(resp.HookSpecificOutput.AdditionalContext))
	}
}
