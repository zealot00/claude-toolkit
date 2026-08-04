package hooks

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/zealot00/claude-toolkit/internal/hudstate"
	"github.com/zealot00/claude-toolkit/internal/payload"
	"github.com/zealot00/claude-toolkit/pkg/dir"
)

// autoproxyFixture wires the test environment so autoproxy behaves the way
// the assertions want it to: env vars isolated, capability enabled (no
// capcfg state file), and the fork / kill seams stubbed.
type autoproxyFixture struct {
	t              *testing.T
	pidPath        string
	forked         atomic.Bool
	lastForkArgs   []string
	lastForkEnv    []string
	killCalls      atomic.Int32
	lastKilledPID  atomic.Int32
	killErr        error
	forkErr        error
	startChildStub func(self, listen, upstream string) (*exec.Cmd, error)
}

func newAutoproxyFixture(t *testing.T) *autoproxyFixture {
	t.Helper()
	t.Setenv(dir.EnvHome, t.TempDir())

	f := &autoproxyFixture{t: t}
	// Stub the self-path seam so we never invoke the real test binary as
	// a child.
	origExecutable := executablePath
	executablePath = func() (string, error) { return "/fake/claude-toolkit", nil }
	t.Cleanup(func() { executablePath = origExecutable })

	// Stub the child-start seam: record what was passed and return a
	// synthesised *exec.Cmd with a fake PID. We never actually Start it
	// (so cmd.Process would be nil); the production code reads
	// cmd.Process.Pid, so we attach a fake Process constructed via
	// os.FindProcess, which on Unix is happy to wrap any integer.
	origStart := startChild
	startChild = func(self, listen, upstream string) (*exec.Cmd, error) {
		f.forked.Store(true)
		f.lastForkArgs = []string{self, "--listen=" + listen, "--upstream=" + upstream}
		if f.forkErr != nil {
			return nil, f.forkErr
		}
		cmd := exec.Command("true")
		if proc, perr := os.FindProcess(99999); perr == nil {
			cmd.Process = proc
		}
		return cmd, nil
	}
	t.Cleanup(func() { startChild = origStart })

	origKill := terminatePID
	terminatePID = func(pid int) error {
		f.killCalls.Add(1)
		f.lastKilledPID.Store(int32(pid))
		return f.killErr
	}
	t.Cleanup(func() { terminatePID = origKill })

	root, err := dir.Root()
	if err != nil {
		t.Fatal(err)
	}
	f.pidPath = filepath.Join(root, "state", "autoproxy.pid")
	return f
}

// withEnv sets env vars for the test and unsets the autoproxy-related ones
// the fixture didn't explicitly set, so a stray shell export cannot leak
// into the assertion.
func (f *autoproxyFixture) withEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	for _, k := range []string{"HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY", "ANTHROPIC_BASE_URL"} {
		_ = os.Unsetenv(k)
	}
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

// sessionStartEvent is the minimum payload Claude Code sends for a
// SessionStart. Other fields are zero, which is fine because autoproxy only
// reads HookEventName, PermissionMode, and (via the payload struct) the rest
// is ignored.
func sessionStartEvent(permMode string) *payload.Event {
	return &payload.Event{
		HookEventName:  payload.EventSessionStart,
		PermissionMode: permMode,
	}
}

func sessionEndEvent() *payload.Event {
	return &payload.Event{
		HookEventName: payload.EventSessionEnd,
	}
}

// pidFileExists reports whether the autoproxy PID file is on disk.
func (f *autoproxyFixture) pidFileExists() bool {
	_, err := os.Stat(f.pidPath)
	return err == nil
}

func (f *autoproxyFixture) writePIDFile(t *testing.T, pid int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(f.pidPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.pidPath, []byte(strconv.Itoa(pid)), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestAutoproxy_AutoModeAndProxy_Forks covers the happy path.
func TestAutoproxy_AutoModeAndProxy_Forks(t *testing.T) {
	f := newAutoproxyFixture(t)
	f.withEnv(t, map[string]string{"HTTPS_PROXY": "http://10.0.0.1:7890"})

	resp, err := autoProxy(context.Background(), sessionStartEvent("bypassPermissions"))
	if err != nil {
		t.Fatalf("autoProxy: %v", err)
	}
	if !f.forked.Load() {
		t.Fatal("expected child fork, none happened")
	}
	if resp == nil || resp.SystemMessage == "" {
		t.Fatal("expected a systemMessage announcing the proxy")
	}
	if !f.pidFileExists() {
		t.Fatal("expected PID file to be written")
	}
	data, err := os.ReadFile(f.pidPath)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(bytes.TrimSpace(data)))
	if err != nil || pid <= 0 {
		t.Fatalf("PID file contents not a positive integer: %q", data)
	}
	// hudstate should reflect the new proxy.
	st, err := hudstate.Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.Proxy == nil {
		t.Fatal("hudstate.Proxy should be set")
	}
	if st.Retry != hudstate.RetryProxy {
		t.Errorf("hudstate.Retry = %q, want %q", st.Retry, hudstate.RetryProxy)
	}
	if st.Mode != "bypassPermissions" {
		t.Errorf("hudstate.Mode = %q, want bypassPermissions", st.Mode)
	}
}

func TestAutoproxy_NoNetworkProxy_DoesNotFork(t *testing.T) {
	f := newAutoproxyFixture(t)
	f.withEnv(t, nil) // no HTTPS_PROXY

	resp, err := autoProxy(context.Background(), sessionStartEvent("bypassPermissions"))
	if err != nil {
		t.Fatalf("autoProxy: %v", err)
	}
	if f.forked.Load() {
		t.Fatal("must not fork without network proxy env")
	}
	if resp != nil {
		t.Fatalf("expected nil response, got %+v", resp)
	}
	if f.pidFileExists() {
		t.Fatal("must not write PID file without fork")
	}
}

func TestAutoproxy_NonAutoMode_DoesNotFork(t *testing.T) {
	f := newAutoproxyFixture(t)
	f.withEnv(t, map[string]string{"HTTPS_PROXY": "http://10.0.0.1:7890"})

	for _, mode := range []string{"default", "acceptEdits", "plan"} {
		f.forked.Store(false)
		_, err := autoProxy(context.Background(), sessionStartEvent(mode))
		if err != nil {
			t.Fatalf("autoProxy %s: %v", mode, err)
		}
		if f.forked.Load() {
			t.Fatalf("mode %s: must not fork", mode)
		}
	}
}

func TestAutoproxy_PIDFileExists_NoRefork(t *testing.T) {
	f := newAutoproxyFixture(t)
	f.withEnv(t, map[string]string{"HTTPS_PROXY": "http://10.0.0.1:7890"})
	f.writePIDFile(t, 99999)

	_, err := autoProxy(context.Background(), sessionStartEvent("bypassPermissions"))
	if err != nil {
		t.Fatalf("autoProxy: %v", err)
	}
	if f.forked.Load() {
		t.Fatal("must not re-fork when PID file already exists")
	}
}

func TestAutoproxy_SessionEnd_KillsPIDAndClearsState(t *testing.T) {
	f := newAutoproxyFixture(t)
	f.withEnv(t, nil)
	f.writePIDFile(t, 4242)

	if _, err := autoProxy(context.Background(), sessionEndEvent()); err != nil {
		t.Fatalf("autoProxy end: %v", err)
	}
	if f.killCalls.Load() != 1 {
		t.Fatalf("kill calls = %d, want 1", f.killCalls.Load())
	}
	if int(f.lastKilledPID.Load()) != 4242 {
		t.Fatalf("killed PID = %d, want 4242", f.lastKilledPID.Load())
	}
	if f.pidFileExists() {
		t.Fatal("PID file should be removed after SessionEnd")
	}
	st, err := hudstate.Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.Proxy != nil {
		t.Errorf("hudstate.Proxy should be cleared, got %+v", st.Proxy)
	}
	if st.Retry != hudstate.RetryOff {
		t.Errorf("hudstate.Retry = %q, want %q", st.Retry, hudstate.RetryOff)
	}
}

func TestAutoproxy_SessionEnd_NoPIDFileIsNoOp(t *testing.T) {
	f := newAutoproxyFixture(t)
	_, err := autoProxy(context.Background(), sessionEndEvent())
	if err != nil {
		t.Fatalf("autoProxy end: %v", err)
	}
	if f.killCalls.Load() != 0 {
		t.Fatalf("must not call kill without PID file, got %d calls", f.killCalls.Load())
	}
}

// TestAutoproxy_ChainedUpstream covers the Kimi/Tencent/Alibaba case where
// the user has ANTHROPIC_BASE_URL pointing at a non-Anthropic endpoint. The
// forked child must use that as its --upstream and must NOT inherit the
// network proxy env (or it would recursively proxy through the user's
// network to reach e.g. api.moonshot.cn).
func TestAutoproxy_ChainedUpstream(t *testing.T) {
	f := newAutoproxyFixture(t)
	f.withEnv(t, map[string]string{
		"HTTPS_PROXY":        "http://10.0.0.1:7890",
		"ANTHROPIC_BASE_URL": "https://api.moonshot.cn/anthropic",
	})

	// Re-stub startChild to also capture env. The fixture's default
	// stub doesn't preserve env; for this test we need to inspect what
	// the child would inherit.
	capturedEnv := []string{}
	origStart := startChild
	startChild = func(self, listen, upstream string) (*exec.Cmd, error) {
		f.forked.Store(true)
		f.lastForkArgs = []string{self, "--listen=" + listen, "--upstream=" + upstream}
		capturedEnv = childEnv()
		cmd := exec.Command("true")
		if proc, perr := os.FindProcess(99999); perr == nil {
			cmd.Process = proc
		}
		return cmd, nil
	}
	t.Cleanup(func() { startChild = origStart })

	if _, err := autoProxy(context.Background(), sessionStartEvent("bypassPermissions")); err != nil {
		t.Fatalf("autoProxy: %v", err)
	}
	// Args should carry the user's moonshot endpoint.
	wantArg := "--upstream=https://api.moonshot.cn/anthropic"
	gotArg := ""
	for _, a := range f.lastForkArgs {
		if a == wantArg {
			gotArg = a
		}
	}
	if gotArg == "" {
		t.Fatalf("expected child args to include %q, got %v", wantArg, f.lastForkArgs)
	}
	// Env should be HTTPS_PROXY-free.
	for _, kv := range capturedEnv {
		name, _, _ := splitOnce(kv, '=')
		switch name {
		case "HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy":
			t.Errorf("child env must not carry %s, got %q", name, kv)
		}
	}
}

// TestAutoproxy_LoopPrevention covers the case where the user has already
// pointed HTTPS_PROXY at the address autoproxy would bind. Forking would be
// recursive.
func TestAutoproxy_LoopPrevention(t *testing.T) {
	f := newAutoproxyFixture(t)
	f.withEnv(t, map[string]string{"HTTPS_PROXY": "http://127.0.0.1:8080"})

	if _, err := autoProxy(context.Background(), sessionStartEvent("bypassPermissions")); err != nil {
		t.Fatalf("autoProxy: %v", err)
	}
	if f.forked.Load() {
		t.Fatal("must not fork when HTTPS_PROXY points at our own listen address")
	}
	if f.pidFileExists() {
		t.Fatal("must not write PID file when refusing to fork")
	}
}

func TestAutoproxy_CapabilityDisabled_NoFork(t *testing.T) {
	f := newAutoproxyFixture(t)
	f.withEnv(t, map[string]string{"HTTPS_PROXY": "http://10.0.0.1:7890"})
	root, err := dir.Root()
	if err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(root, "state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "capabilities.json"),
		[]byte(`{"enabled":{"autoproxy":false}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := autoProxy(context.Background(), sessionStartEvent("bypassPermissions")); err != nil {
		t.Fatalf("autoProxy: %v", err)
	}
	if f.forked.Load() {
		t.Fatal("must not fork when capability disabled")
	}
}

func TestAutoproxy_ForkError_ReturnsMessageAndOFFState(t *testing.T) {
	f := newAutoproxyFixture(t)
	f.withEnv(t, map[string]string{"HTTPS_PROXY": "http://10.0.0.1:7890"})

	origStart := startChild
	startChild = func(self, listen, upstream string) (*exec.Cmd, error) {
		return nil, errors.New("synthetic fork failure")
	}
	t.Cleanup(func() { startChild = origStart })

	resp, err := autoProxy(context.Background(), sessionStartEvent("bypassPermissions"))
	if err != nil {
		t.Fatalf("autoProxy: %v", err)
	}
	if resp == nil || resp.SystemMessage == "" {
		t.Fatal("expected a systemMessage describing the fork failure")
	}
	if f.pidFileExists() {
		t.Fatal("PID file must not exist after fork failure")
	}
	st, err := hudstate.Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.Proxy != nil {
		t.Errorf("hudstate.Proxy should not be set on fork failure, got %+v", st.Proxy)
	}
	if st.Retry != hudstate.RetryOff {
		t.Errorf("hudstate.Retry = %q, want %q", st.Retry, hudstate.RetryOff)
	}
}

// splitOnce is a minimal strings.Cut equivalent used only in this test
// (avoids importing strings just for one call site). It returns (before,
// after, true) when sep appears; ("", "", false) otherwise.
func splitOnce(s string, sep byte) (string, string, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			return s[:i], s[i+1:], true
		}
	}
	return "", "", false
}

// Sanity: the synthesised *exec.Cmd has Process set so Process.Pid is read.
var _ = exec.Command
