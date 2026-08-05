package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zealot00/claude-toolkit/pkg/installer"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.1.0", "0.1.0", 0},
		{"0.1.0", "0.2.0", -1},
		{"1.0.0", "0.9.9", 1},
		{"0.10.0", "0.9.0", 1},
		{"1.2", "1.2.0", 0},
		{"1.2.3", "1.2", 1},
		{"dev", "0.1.0", -1}, // non-numeric sorts low
	}
	for _, tc := range cases {
		if got := compareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestAstCmdOutput(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "sample.go")
	if err := os.WriteFile(src, []byte("package sample\n\nfunc Hello(name string) string { return name }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Capture stdout by replacing it for the duration of the call.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	code := astCmd([]string{src})
	w.Close()
	os.Stdout = old

	if code != 0 {
		t.Fatalf("astCmd exit = %d", code)
	}
	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	out := string(buf[:n])
	if !strings.Contains(out, `"package": "sample"`) || !strings.Contains(out, "Hello") {
		t.Errorf("ast output missing package/func: %s", out)
	}
}

func TestAstCmdRejectsUnknownExt(t *testing.T) {
	if code := astCmd([]string{"x.ts"}); code != 2 {
		t.Errorf("astCmd(x.ts) = %d, want 2", code)
	}
}

func TestRulesCmdOutput(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	code := rulesCmd(nil)
	w.Close()
	os.Stdout = old

	if code != 0 {
		t.Fatalf("rulesCmd exit = %d", code)
	}
	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	out := string(buf[:n])
	for _, want := range []string{"rm-rf-root", "high-entropy-secret", "protected-branch", "log-dump"} {
		if !strings.Contains(out, want) {
			t.Errorf("rules output missing %q", want)
		}
	}
}

func TestUninstallCmdRemovesHooks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude", "settings.json")
	// Install first via the same machinery manage uses.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	plan, err := installer.BuildPlan(path, buildSpecs("claude-toolkit"))
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}

	if code := uninstallCmd([]string{"--scope=project", "--project-dir=" + dir}); code != 0 {
		t.Fatalf("uninstall exit = %d", code)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "claude-toolkit") {
		t.Error("settings still contains claude-toolkit entries after uninstall")
	}
}

// TestUsageListsAllCommands pins the --help output: every subcommand the
// switch in Execute can dispatch must be discoverable from usage(). A
// command omitted here is invisible to `claude-toolkit --help`, and agents
// (including Claude Code via /toolkit) then conclude it does not exist.
func TestUsageListsAllCommands(t *testing.T) {
	var buf bytes.Buffer
	usage(&buf)
	out := buf.String()
	want := []string{
		"init", "manage", "model", "test", "ast", "rules", "proxy",
		"upgrade", "uninstall", "log", "doctor", "hud", "run", "version",
	}
	for _, w := range want {
		if !strings.Contains(out, "  "+w) {
			t.Errorf("usage() missing %q command entry\nfull output:\n%s", w, out)
		}
	}
}
