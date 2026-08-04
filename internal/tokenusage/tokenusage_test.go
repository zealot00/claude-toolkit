package tokenusage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTranscript drops fixture lines into a temp file and returns its path.
func writeTranscript(t *testing.T, lines ...string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")
	if len(lines) == 0 {
		// Touch an empty file rather than skipping the write -- an empty
		// transcript is still a valid input (zero Summary).
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

func TestSummarize_EmptyPath(t *testing.T) {
	if got := Summarize(""); got.Total != 0 {
		t.Fatalf("empty path: got %+v, want zero", got)
	}
}

func TestSummarize_MissingFile(t *testing.T) {
	if got := Summarize("/nonexistent/path/to/transcript"); got.Total != 0 {
		t.Fatalf("missing file: got %+v, want zero", got)
	}
}

func TestSummarize_EmptyFile(t *testing.T) {
	path := writeTranscript(t)
	if got := Summarize(path); got.Total != 0 {
		t.Fatalf("empty file: got %+v, want zero", got)
	}
}

func TestSummarize_SingleAssistantTurn(t *testing.T) {
	path := writeTranscript(t, `{"type":"user","message":{"role":"user","content":"hi"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"text":"hello"}],"usage":{"input_tokens":100,"cache_creation_input_tokens":50,"cache_read_input_tokens":200,"output_tokens":30}}}`)
	got := Summarize(path)
	want := Summary{Input: 100, CacheCreation: 50, CacheRead: 200, Output: 30, Total: 380}
	if got != want {
		t.Fatalf("single turn: got %+v, want %+v", got, want)
	}
}

func TestSummarize_MultipleAssistantTurnsAccumulate(t *testing.T) {
	path := writeTranscript(t,
		`{"type":"assistant","message":{"usage":{"input_tokens":10,"output_tokens":5}}}`,
		`{"type":"user","message":{"role":"user","content":"again"}}`,
		`{"type":"assistant","message":{"usage":{"input_tokens":20,"cache_read_input_tokens":15,"output_tokens":7}}}`)
	got := Summarize(path)
	want := Summary{Input: 30, CacheCreation: 0, CacheRead: 15, Output: 12, Total: 57}
	if got != want {
		t.Fatalf("multi turn: got %+v, want %+v", got, want)
	}
}

func TestSummarize_CorruptLinesIgnored(t *testing.T) {
	path := writeTranscript(t,
		`{this is not valid json`,
		`{"type":"assistant","message":{"usage":{"input_tokens":100,"output_tokens":10}}}`,
		`{"type":"assistant","usage":"malformed"`, // truncated
		``)
	got := Summarize(path)
	want := Summary{Input: 100, Output: 10, Total: 110}
	if got != want {
		t.Fatalf("corrupt lines: got %+v, want %+v", got, want)
	}
}

func TestSummarize_NonAssistantLinesIgnored(t *testing.T) {
	path := writeTranscript(t,
		`{"type":"user","message":{"role":"user","content":"hi","usage":{"input_tokens":999}}}`,
		`{"type":"system","message":{"usage":{"input_tokens":999}}}`,
		`{"type":"assistant","message":{"usage":{"input_tokens":42,"output_tokens":7}}}`)
	got := Summarize(path)
	want := Summary{Input: 42, Output: 7, Total: 49}
	if got != want {
		t.Fatalf("non-assistant ignored: got %+v, want %+v", got, want)
	}
}

func TestSummarize_AssistantWithoutUsageIgnored(t *testing.T) {
	// An assistant turn with content but no usage block contributes nothing
	// (e.g. tool-call-only responses). Real transcripts emit usage on every
	// assistant turn today, but older or alternate formats may not.
	path := writeTranscript(t,
		`{"type":"assistant","message":{"content":[{"text":"hi"}]}}`,
		`{"type":"assistant","message":{"usage":{"input_tokens":1,"output_tokens":1}}}`)
	got := Summarize(path)
	want := Summary{Input: 1, Output: 1, Total: 2}
	if got != want {
		t.Fatalf("no-usage assistant ignored: got %+v, want %+v", got, want)
	}
}
