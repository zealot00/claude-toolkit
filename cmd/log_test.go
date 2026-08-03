package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTailLinesOf(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.txt")
	lines := make([]string, 0, 60)
	for i := 0; i < 60; i++ {
		lines = append(lines, "line "+string(rune('a'+i%26))+" "+strings.Repeat("x", i%7))
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var out bytes.Buffer
	if code := tailLinesOf(&out, f, 10, ""); code != 0 {
		t.Fatalf("exit code %d", code)
	}
	got := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(got) != 10 {
		t.Fatalf("want 10 tail lines, got %d: %q", len(got), out.String())
	}
	if !strings.Contains(got[0], "line ") {
		t.Errorf("tail content wrong: %q", got[0])
	}

	// Event filter keeps only matching lines.
	out.Reset()
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	if code := tailLinesOf(&out, f, 50, "a"); code != 0 {
		t.Fatalf("exit code %d", code)
	}
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if !strings.Contains(l, "a") {
			t.Errorf("event filter leaked a non-matching line: %q", l)
		}
	}
}
