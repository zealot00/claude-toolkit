package cmd

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// withEnv sets and restores env vars around a test body.
func withEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		old, had := os.LookupEnv(k)
		os.Setenv(k, v)
		t.Cleanup(func() {
			if had {
				os.Setenv(k, old)
			} else {
				os.Unsetenv(k)
			}
		})
	}
}

// withArgs overrides os.Args for the test and restores it on cleanup. The
// hint check inspects os.Args[1] to decide whether the user invoked a hook.
func withArgs(t *testing.T, args ...string) {
	t.Helper()
	old := os.Args
	os.Args = args
	t.Cleanup(func() { os.Args = old })
}

func TestMaybeEmitHintSuppressedOutsideClaudeCode(t *testing.T) {
	withEnv(t, map[string]string{"CLAUDECODE": "", "CLAUDE_CODE_ENTRYPOINT": ""})
	withArgs(t, "claude-toolkit", "version")

	var buf bytes.Buffer
	origStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	defer func() { os.Stderr = origStderr }()

	maybeEmitHint()
	w.Close()
	buf.ReadFrom(r)

	if strings.Contains(buf.String(), "<claude-code-hint") {
		t.Errorf("hint emitted outside Claude Code: %q", buf.String())
	}
}

func TestMaybeEmitHintEmittedInsideClaudeCode(t *testing.T) {
	withEnv(t, map[string]string{"CLAUDECODE": "1"})
	withArgs(t, "claude-toolkit", "version")

	var buf bytes.Buffer
	r, w, _ := os.Pipe()
	origStderr := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = origStderr }()

	maybeEmitHint()
	w.Close()
	buf.ReadFrom(r)

	got := buf.String()
	if !strings.Contains(got, `<claude-code-hint v="1"`) {
		t.Errorf("hint missing in output: %q", got)
	}
	if !strings.Contains(got, pluginHintValue) {
		t.Errorf("hint value missing %q: %q", pluginHintValue, got)
	}
}

func TestMaybeEmitHintSuppressedDuringHook(t *testing.T) {
	withEnv(t, map[string]string{"CLAUDECODE": "1"})
	withArgs(t, "claude-toolkit", "run", "--event=pre")

	var buf bytes.Buffer
	r, w, _ := os.Pipe()
	origStderr := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = origStderr }()

	maybeEmitHint()
	w.Close()
	buf.ReadFrom(r)

	if strings.Contains(buf.String(), "<claude-code-hint") {
		t.Errorf("hint emitted during hook invocation: %q", buf.String())
	}
}

func TestMaybeEmitHintAcceptsAlternateEnvSignal(t *testing.T) {
	withEnv(t, map[string]string{"CLAUDECODE": "", "CLAUDE_CODE_ENTRYPOINT": "cli"})
	withArgs(t, "claude-toolkit", "doctor")

	var buf bytes.Buffer
	r, w, _ := os.Pipe()
	origStderr := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = origStderr }()

	maybeEmitHint()
	w.Close()
	buf.ReadFrom(r)

	if !strings.Contains(buf.String(), "<claude-code-hint") {
		t.Errorf("hint missing with CLAUDE_CODE_ENTRYPOINT: %q", buf.String())
	}
}
