package hooks

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zealot00/claude-toolkit/internal/payload"
)

func postWrite(t *testing.T, path string) *payload.Event {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"file_path": path})
	if err != nil {
		t.Fatal(err)
	}
	return &payload.Event{
		HookEventName: payload.EventPostToolUse,
		ToolName:      "Write",
		ToolInput:     raw,
	}
}

func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHealGoWithSiblingTests(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "widget.go")
	writeFixture(t, src, "package widget\n")
	writeFixture(t, filepath.Join(dir, "widget_test.go"), "package widget\n")

	resp, err := heal(context.Background(), postWrite(t, src))
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil || resp.HookSpecificOutput == nil || resp.HookSpecificOutput.AdditionalContext == "" {
		t.Fatal("expected a hint when a sibling _test.go exists")
	}
	if !strings.Contains(resp.HookSpecificOutput.AdditionalContext, "claude-toolkit test") {
		t.Errorf("hint should point at the test command: %q", resp.HookSpecificOutput.AdditionalContext)
	}
}

func TestHealGoWithoutTestsSilent(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "widget.go")
	writeFixture(t, src, "package widget\n")

	resp, err := heal(context.Background(), postWrite(t, src))
	if err != nil {
		t.Fatal(err)
	}
	if resp != nil {
		t.Errorf("no _test.go in the directory: expected no opinion, got %+v", resp)
	}
}

func TestHealPyWithTestFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "util.py")
	writeFixture(t, src, "x = 1\n")
	writeFixture(t, filepath.Join(dir, "test_util.py"), "def test_x(): pass\n")

	resp, err := heal(context.Background(), postWrite(t, src))
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil || resp.HookSpecificOutput == nil || resp.HookSpecificOutput.AdditionalContext == "" {
		t.Fatal("expected a hint when a pytest target exists")
	}
}

func TestHealPyWithoutTestFileSilent(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "util.py")
	writeFixture(t, src, "x = 1\n")

	resp, err := heal(context.Background(), postWrite(t, src))
	if err != nil {
		t.Fatal(err)
	}
	if resp != nil {
		t.Errorf("no pytest target: expected no opinion, got %+v", resp)
	}
}

func TestHealUnsupportedExtSilent(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "styles.css")
	writeFixture(t, src, "body{}\n")

	resp, err := heal(context.Background(), postWrite(t, src))
	if err != nil {
		t.Fatal(err)
	}
	if resp != nil {
		t.Errorf("non-go/py file: expected no opinion, got %+v", resp)
	}
}
