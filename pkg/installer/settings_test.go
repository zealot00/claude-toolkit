package installer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// realWorldSettings mirrors the shape of an actual ~/.claude/settings.json:
// an API credential, model overrides, plugins, and a hook the user configured
// by hand. Every one of these must survive an install untouched.
const realWorldSettings = `{
  "env": {
    "ANTHROPIC_BASE_URL": "https://api.example.com/anthropic",
    "ANTHROPIC_AUTH_TOKEN": "sk-cp-SECRET-VALUE-MUST-SURVIVE",
    "API_TIMEOUT_MS": "3000000",
    "CLAUDE_CODE_AUTO_COMPACT_WINDOW": "512000"
  },
  "enabledPlugins": {
    "gopls-lsp@claude-plugins-official": true
  },
  "model": "some-model",
  "maxTokens": 8192,
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {"type": "command", "command": "/Users/me/my-own-guard.sh"}
        ]
      }
    ],
    "Stop": [
      {
        "matcher": "*",
        "hooks": [
          {"type": "command", "command": "notify-send done"}
        ]
      }
    ]
  }
}`

func specs() []Spec {
	return []Spec{
		{Event: "PreToolUse", Capability: "guard", Matcher: "Bash|Write", Command: "claude-toolkit run --event=pre --cap=guard", Timeout: 10},
		{Event: "SessionStart", Capability: "enrich", Matcher: "*", Command: "claude-toolkit run --event=session --cap=enrich", Timeout: 15},
	}
}

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if content != "" {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func applyPlan(t *testing.T, path string, s []Spec) *Plan {
	t.Helper()
	p, err := BuildPlan(path, s)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if err := p.Apply(); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return p
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	return m
}

// TestPreservesUnrelatedSettings is the test this package exists for. Losing
// the auth token would mean a user reinstalling the toolkit gets logged out.
func TestPreservesUnrelatedSettings(t *testing.T) {
	path := writeTemp(t, realWorldSettings)
	applyPlan(t, path, specs())

	got := readJSON(t, path)

	env, ok := got["env"].(map[string]any)
	if !ok {
		t.Fatal("env block was lost")
	}
	if env["ANTHROPIC_AUTH_TOKEN"] != "sk-cp-SECRET-VALUE-MUST-SURVIVE" {
		t.Errorf("auth token was altered: %v", env["ANTHROPIC_AUTH_TOKEN"])
	}
	if env["ANTHROPIC_BASE_URL"] != "https://api.example.com/anthropic" {
		t.Errorf("base URL was altered: %v", env["ANTHROPIC_BASE_URL"])
	}
	if got["model"] != "some-model" {
		t.Errorf("model was altered: %v", got["model"])
	}
	if _, ok := got["enabledPlugins"].(map[string]any); !ok {
		t.Error("enabledPlugins was lost")
	}
	// UseNumber must keep this an integer literal, not 8.192e+03.
	if n, ok := got["maxTokens"].(json.Number); !ok || n.String() != "8192" {
		t.Errorf("numeric value was mangled: %#v", got["maxTokens"])
	}
}

// TestPreservesForeignHooks covers the failure mode of assigning the whole
// "hooks" key: the user's own hooks silently disappear.
func TestPreservesForeignHooks(t *testing.T) {
	path := writeTemp(t, realWorldSettings)
	applyPlan(t, path, specs())

	got := readJSON(t, path)
	hooks := got["hooks"].(map[string]any)

	// The hand-written PreToolUse hook must still be there, alongside ours.
	pre := hooks["PreToolUse"].([]any)
	var foundForeign, foundOurs bool
	for _, g := range pre {
		entries := g.(map[string]any)["hooks"].([]any)
		for _, e := range entries {
			cmd := e.(map[string]any)["command"].(string)
			if cmd == "/Users/me/my-own-guard.sh" {
				foundForeign = true
			}
			if IsOwned(cmd) {
				foundOurs = true
			}
		}
	}
	if !foundForeign {
		t.Error("the user's own PreToolUse hook was removed")
	}
	if !foundOurs {
		t.Error("our PreToolUse hook was not installed")
	}

	// An event we do not touch must be completely untouched.
	stop := hooks["Stop"].([]any)
	if len(stop) != 1 {
		t.Fatalf("Stop hooks were modified: %v", stop)
	}
	if cmd := stop[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)["command"]; cmd != "notify-send done" {
		t.Errorf("Stop hook was altered: %v", cmd)
	}
}

// TestIdempotent guards against re-running init stacking up duplicate entries.
func TestIdempotent(t *testing.T) {
	path := writeTemp(t, realWorldSettings)
	applyPlan(t, path, specs())
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	p, err := BuildPlan(path, specs())
	if err != nil {
		t.Fatal(err)
	}
	if p.Changed() {
		t.Errorf("second install reports a change; it should be a no-op:\n%s", p.Summary())
	}
	if err := p.Apply(); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Error("second install altered the file")
	}
}

// TestReplacesMovedBinary covers upgrading from a PATH-based entry to an
// absolute one (or the reverse): the stale entry must go, not accumulate.
func TestReplacesMovedBinary(t *testing.T) {
	path := writeTemp(t, realWorldSettings)
	applyPlan(t, path, specs())

	moved := []Spec{
		{Event: "PreToolUse", Matcher: "Bash|Write", Command: "/opt/bin/claude-toolkit run --event=pre", Timeout: 10},
		{Event: "SessionStart", Matcher: "*", Command: "/opt/bin/claude-toolkit run --event=session", Timeout: 15},
	}
	applyPlan(t, path, moved)

	got := readJSON(t, path)
	pre := got["hooks"].(map[string]any)["PreToolUse"].([]any)
	var ours []string
	for _, g := range pre {
		for _, e := range g.(map[string]any)["hooks"].([]any) {
			cmd := e.(map[string]any)["command"].(string)
			if IsOwned(cmd) {
				ours = append(ours, cmd)
			}
		}
	}
	if len(ours) != 1 {
		t.Fatalf("want exactly 1 owned entry after re-install, got %d: %v", len(ours), ours)
	}
	if !strings.HasPrefix(ours[0], "/opt/bin/") {
		t.Errorf("stale entry survived: %q", ours[0])
	}
}

// TestUninstallLeavesForeignHooks checks removal is as surgical as install.
func TestUninstallLeavesForeignHooks(t *testing.T) {
	path := writeTemp(t, realWorldSettings)
	applyPlan(t, path, specs())
	applyPlan(t, path, nil) // no specs == uninstall

	got := readJSON(t, path)
	hooks := got["hooks"].(map[string]any)

	if _, ok := hooks["SessionStart"]; ok {
		t.Error("SessionStart should have been removed entirely; only our hook was there")
	}
	pre := hooks["PreToolUse"].([]any)
	if len(pre) != 1 {
		t.Fatalf("want the user's single PreToolUse group to remain, got %d", len(pre))
	}
	cmd := pre[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)["command"]
	if cmd != "/Users/me/my-own-guard.sh" {
		t.Errorf("the user's hook did not survive uninstall: %v", cmd)
	}
	if env := got["env"].(map[string]any); env["ANTHROPIC_AUTH_TOKEN"] == nil {
		t.Error("uninstall dropped the auth token")
	}
}

// TestRefusesInvalidJSON is the rule that a file we cannot parse is a file we
// must not overwrite.
func TestRefusesInvalidJSON(t *testing.T) {
	const broken = `{"env": {"TOKEN": "abc",}}` // trailing comma
	path := writeTemp(t, broken)

	if _, err := BuildPlan(path, specs()); err == nil {
		t.Fatal("BuildPlan accepted invalid JSON; it must refuse")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != broken {
		t.Error("the unparseable file was modified")
	}
}

// TestRefusesWrongShape covers a "hooks" key holding something that is not an
// object -- another case where guessing would destroy data.
func TestRefusesWrongShape(t *testing.T) {
	path := writeTemp(t, `{"hooks": "not-an-object"}`)
	if _, err := BuildPlan(path, specs()); err == nil {
		t.Fatal("BuildPlan accepted a non-object hooks key; it must refuse")
	}
}

// TestCreatesMissingFile covers a fresh machine, where the file does not exist.
func TestCreatesMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "settings.json")
	p := applyPlan(t, path, specs())

	if p.BackupPath != "" {
		t.Error("nothing existed, so nothing should have been backed up")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("file was not created: %v", err)
	}
	// A file we create will hold an API token; it must not be world-readable.
	// Windows has no POSIX permission bits; the 0600 contract is enforced by
	// the code path that creates the file and is asserted on POSIX only.
	if mode := info.Mode().Perm(); runtime.GOOS != "windows" && mode != 0o600 {
		t.Errorf("new settings file mode is %#o, want 0600", mode)
	}
	got := readJSON(t, path)
	if _, ok := got["hooks"].(map[string]any)["PreToolUse"]; !ok {
		t.Error("hooks were not written")
	}
}

// TestBacksUpBeforeWriting checks the recovery path exists and is faithful.
func TestBacksUpBeforeWriting(t *testing.T) {
	path := writeTemp(t, realWorldSettings)
	p := applyPlan(t, path, specs())

	if p.BackupPath == "" {
		t.Fatal("no backup was taken before modifying an existing file")
	}
	backup, err := os.ReadFile(p.BackupPath)
	if err != nil {
		t.Fatalf("backup not written: %v", err)
	}
	if string(backup) != realWorldSettings {
		t.Error("backup does not match the original file")
	}
}

// TestPreservesFileMode checks we do not silently loosen or tighten perms on
// a file the user already configured.
func TestPreservesFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no POSIX permission semantics")
	}
	path := writeTemp(t, realWorldSettings)
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	applyPlan(t, path, specs())

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("mode changed to %#o, want 0600 preserved", mode)
	}
}

func TestIsOwned(t *testing.T) {
	owned := []string{
		"claude-toolkit run --event=pre",
		"claude-toolkit run --event=pre --cap=guard",
		"claude-toolkit run",
		"/usr/local/bin/claude-toolkit run --event=post --cap=format",
		`"/Users/me/go/bin/claude-toolkit" run --event=session`,
		`C:\tools\claude-toolkit.exe run --event=pre --cap=guard`,
	}
	for _, c := range owned {
		if !IsOwned(c) {
			t.Errorf("IsOwned(%q) = false, want true", c)
		}
	}

	foreign := []string{
		"/Users/me/my-own-guard.sh",
		"notify-send done",
		"claude-toolkit-other run",
		"echo claude-toolkit running",
		"my-claude-toolkit-wrapper.sh",
	}
	for _, c := range foreign {
		if IsOwned(c) {
			t.Errorf("IsOwned(%q) = true, want false", c)
		}
	}
}

func TestInspect(t *testing.T) {
	path := writeTemp(t, realWorldSettings)
	applyPlan(t, path, specs())

	got, err := Inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 installed entries, got %d: %+v", len(got), got)
	}
	byEvent := map[string]Installed{}
	for _, e := range got {
		byEvent[e.Event] = e
	}
	if e := byEvent["PreToolUse"]; e.Matcher != "Bash|Write" || e.Timeout != 10 || e.Capability != "guard" {
		t.Errorf("PreToolUse entry wrong: %+v", e)
	}
	if e := byEvent["SessionStart"]; e.Timeout != 15 || e.Capability != "enrich" {
		t.Errorf("SessionStart entry wrong: %+v", e)
	}
}

func TestInspectMissingFile(t *testing.T) {
	got, err := Inspect(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("a missing file should not be an error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want no entries, got %+v", got)
	}
}

// TestMultipleCapabilitiesSameEvent pins the case where two capabilities
// (format, heal) share one event: each must get its own matcher group, so
// disabling one does not take down the other.
func TestMultipleCapabilitiesSameEvent(t *testing.T) {
	path := writeTemp(t, "")
	sameEvent := []Spec{
		{Event: "PostToolUse", Capability: "format", Matcher: "Write|Edit", Command: "claude-toolkit run --event=post --cap=format", Timeout: 30},
		{Event: "PostToolUse", Capability: "heal", Matcher: "Write|Edit", Command: "claude-toolkit run --event=post --cap=heal", Timeout: 30},
	}
	applyPlan(t, path, sameEvent)

	entries, err := Inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("want both capabilities installed, got %d: %+v", len(entries), entries)
	}
	seen := map[string]bool{}
	for _, e := range entries {
		seen[e.Capability] = true
	}
	if !seen["format"] || !seen["heal"] {
		t.Fatalf("both capabilities must be present: %v", seen)
	}

	// Disabling one must leave the other's group in place.
	onlyHeal := []Spec{
		{Event: "PostToolUse", Capability: "heal", Matcher: "Write|Edit", Command: "claude-toolkit run --event=post --cap=heal", Timeout: 30},
	}
	applyPlan(t, path, onlyHeal)
	entries, err = Inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Capability != "heal" {
		t.Fatalf("after disabling format, want exactly heal, got %+v", entries)
	}

	// Re-adding format is idempotent with the original full set.
	applyPlan(t, path, sameEvent)
	entries, err = Inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 entries after re-adding, got %d: %+v", len(entries), entries)
	}
}

func TestCapabilityOf(t *testing.T) {
	cases := map[string]string{
		"claude-toolkit run --event=pre --cap=guard":                    "guard",
		`"/usr/local/bin/claude-toolkit" run --event=post --cap=format`: "format",
		`C:\tools\claude-toolkit.exe run --event=pre --cap=guard`:       "guard",
		"claude-toolkit run --event=pre":                                "", // legacy, pre-capability
		"notify-send done":                                              "",
	}
	for cmd, want := range cases {
		if got := CapabilityOf(cmd); got != want {
			t.Errorf("CapabilityOf(%q) = %q, want %q", cmd, got, want)
		}
	}
}
