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

func bashCmdEvent(cmd, cwd string) *payload.Event {
	raw, _ := json.Marshal(map[string]string{"command": cmd})
	return &payload.Event{
		HookEventName: payload.EventPreToolUse,
		ToolName:      "Bash",
		ToolInput:     raw,
		Cwd:           cwd,
	}
}

func TestEnvFixRewritesToVenv(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".venv", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".venv", "bin", "pytest"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	resp, err := envFix(context.Background(), bashCmdEvent("pytest -x tests/", dir))
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil || resp.HookSpecificOutput == nil {
		t.Fatal("expected a rewrite response")
	}
	if resp.HookSpecificOutput.PermissionDecision != payload.DecisionAllow {
		t.Errorf("decision = %q, want allow", resp.HookSpecificOutput.PermissionDecision)
	}
	var updated struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(resp.HookSpecificOutput.UpdatedInput, &updated); err != nil {
		t.Fatalf("UpdatedInput not valid BashInput JSON: %v", err)
	}
	want := filepath.Join(dir, ".venv", "bin", "pytest") + " -x tests/"
	if updated.Command != want {
		t.Errorf("rewritten command = %q, want %q", updated.Command, want)
	}
}

func TestEnvFixNoVenvSilent(t *testing.T) {
	dir := t.TempDir() // no .venv
	resp, err := envFix(context.Background(), bashCmdEvent("pytest -x", dir))
	if err != nil {
		t.Fatal(err)
	}
	if resp != nil {
		t.Errorf("no venv: expected no opinion, got %+v", resp)
	}
}

func TestEnvFixSkipsPathsAndComplexCommands(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".venv", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []string{
		"/usr/bin/python -m pytest", // absolute path
		"./tools/pytest -x",         // relative path
		"pytest -x && npm test",     // compound
		"cat x | pytest",            // pipe
		"echo $(pytest -x)",         // substitution
		"npm test",                  // not a venv target when npm exists? npm IS a target, but no npm binary in venv
	}
	for _, c := range cases {
		resp, err := envFix(context.Background(), bashCmdEvent(c, dir))
		if err != nil {
			t.Fatalf("%q: %v", c, err)
		}
		if resp != nil {
			t.Errorf("%q: expected no rewrite, got %+v", c, resp)
		}
	}
}

func TestEnvFixRewritesArgsKept(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".venv", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".venv", "bin", "python"), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	resp, err := envFix(context.Background(), bashCmdEvent("python run.py --debug", dir))
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil || resp.HookSpecificOutput == nil {
		t.Fatal("expected a rewrite for python")
	}
	var updated struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(resp.HookSpecificOutput.UpdatedInput, &updated)
	if !strings.Contains(updated.Command, "run.py") || !strings.Contains(updated.Command, "--debug") {
		t.Errorf("args lost in rewrite: %q", updated.Command)
	}
}
