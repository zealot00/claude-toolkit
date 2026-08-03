package hooks

import (
	"strings"
	"testing"
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
