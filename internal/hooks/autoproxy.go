package hooks

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/zealot00/claude-toolkit/internal/capcfg"
	"github.com/zealot00/claude-toolkit/internal/dispatcher"
	"github.com/zealot00/claude-toolkit/internal/hudstate"
	"github.com/zealot00/claude-toolkit/internal/payload"
	"github.com/zealot00/claude-toolkit/internal/proxy"
	"github.com/zealot00/claude-toolkit/internal/proxydetect"
	"github.com/zealot00/claude-toolkit/pkg/dir"
)

// Autoproxy is the "autoproxy" capability. It bridges the network proxy the
// user has already opted into (HTTPS_PROXY / HTTP_PROXY / ALL_PROXY) and the
// toolkit's optional 429 retry proxy.
//
// Why it exists: PLAN.md §9 explicitly rejected auto-injecting
// ANTHROPIC_BASE_URL because a dead proxy would take Claude Code down. The
// user's NETWORK proxy env is a strong signal they have already accepted
// proxy-failure as a trade-off -- so when it is set, we layer our LLM-side
// retry proxy on top and tell the user how to opt their traffic through it.
// When it is not set, autoproxy stays out of the way and retryguard handles
// 429 fallback via Stop-hook blocking instead.
//
// Lifecycle:
//   - SessionStart (auto mode + network proxy set): fork `proxy` subcommand
//     and record its PID.
//   - SessionEnd: read the PID file, terminate the child, clear the PID file.
//
// The child process inherits neither HTTPS_PROXY/HTTP_PROXY/ALL_PROXY nor any
// other env var that could cause recursive proxying: our retry proxy must
// reach its upstream directly. If the user has already pointed HTTPS_PROXY
// at our listen port (the obvious "I want Claude Code to use this" setup)
// we treat that as a loop and refuse to fork.
func Autoproxy() *dispatcher.Route {
	return &dispatcher.Route{
		Name:    "autoproxy",
		Events:  []string{payload.EventSessionStart, payload.EventSessionEnd},
		Handler: autoProxy,
	}
}

// autoProxyListen is the local address the auto-spawned child binds. Keep it
// identical to proxy.DefaultListen so the user can use the value they are
// already used to from the manual `claude-toolkit proxy` command.
const autoProxyListen = proxy.DefaultListen // 127.0.0.1:8080

// Seams for tests. Production code calls os.Executable, exec.Command, and
// syscall.Kill; tests stub these to assert wiring without spawning real
// processes or terminating the test binary.
var (
	executablePath = os.Executable
	startChild     = startProxyChild
	terminatePID   = defaultTerminatePID
)

// startProxyChild builds the exec.Cmd for the proxy child and starts it.
// Kept as a package var so tests can substitute a recorder. The cmd's env
// is stripped of HTTPS_PROXY/HTTP_PROXY/ALL_PROXY so the child reaches its
// upstream directly even when the parent is configured to use a network
// proxy.
func startProxyChild(self, listen, upstream string) (*exec.Cmd, error) {
	cmd := exec.Command(self, "proxy",
		"--listen="+listen,
		"--upstream="+upstream,
	)
	cmd.Env = childEnv()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

// childEnv returns the inherited environment with proxy keys removed.
// Inheriting everything else keeps the child portable across shells and CI
// (PATH, HOME, locale, TLS certs, etc.).
func childEnv() []string {
	strip := map[string]bool{
		"HTTPS_PROXY": true, "https_proxy": true,
		"HTTP_PROXY": true, "http_proxy": true,
		"ALL_PROXY": true, "all_proxy": true,
	}
	var out []string
	for _, kv := range os.Environ() {
		name, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if strip[name] {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// defaultTerminatePID sends SIGTERM to the process group rooted at pid, so
// any grandchildren are also signalled. Returns an error so callers can
// fall back to SIGKILL if needed.
func defaultTerminatePID(pid int) error {
	return syscall.Kill(-pid, syscall.SIGTERM)
}

func autoProxy(ctx context.Context, e *payload.Event) (*payload.Response, error) {
	disabled, err := capcfg.Disabled()
	if err != nil {
		return nil, nil // fail open: misconfigured state must not break the session
	}
	if disabled["autoproxy"] {
		return nil, nil
	}

	switch e.HookEventName {
	case payload.EventSessionStart:
		return autoProxyStart(ctx, e)
	case payload.EventSessionEnd:
		return autoProxyEnd(ctx, e)
	}
	return nil, nil
}

func autoProxyStart(ctx context.Context, e *payload.Event) (*payload.Response, error) {
	if e.PermissionMode != "bypassPermissions" {
		return nil, nil // auto mode only; outside it the user is in control
	}
	netProxy, ok := proxydetect.Detect()
	if !ok {
		return nil, nil // no network proxy -> let retryguard handle it
	}

	// Loop prevention: if the user has already pointed HTTPS_PROXY at our
	// listen address, forking a second proxy behind the first is recursive.
	if refersToLocalProxy(netProxy, autoProxyListen) {
		return nil, nil
	}

	upstream := proxy.DefaultUpstream
	if v := os.Getenv("ANTHROPIC_BASE_URL"); v != "" {
		upstream = v // chain: keep the user's non-Anthropic endpoint intact
	}

	pidPath, err := autoProxyPIDPath()
	if err != nil {
		return nil, nil
	}
	if _, err := os.Stat(pidPath); err == nil {
		// Previous session left a PID file behind (e.g. crashed before
		// SessionEnd fired). Refuse to double-fork; the stale PID will be
		// cleaned up on the next SessionEnd or by `claude-toolkit proxy
		// --cleanup`.
		return nil, nil
	}

	self, err := executablePath()
	if err != nil || self == "" {
		return nil, nil
	}

	if left, ok := dispatcher.Remaining(ctx); ok && left < 2*time.Second {
		return nil, nil // no time to fork; retryguard is the fallback
	}

	cmd, err := startChild(self, autoProxyListen, upstream)
	if err != nil {
		// Fork failed: tell the user we tried, leave hudstate OFF so
		// retryguard can take over.
		_ = hudstate.Save(hudstate.State{
			Retry: hudstate.RetryOff,
			Mode:  e.PermissionMode,
		})
		return &payload.Response{
			SystemMessage: fmt.Sprintf(
				"autoproxy: failed to start 429 retry proxy on %s: %v (falling back to stop-hook retry)",
				autoProxyListen, err),
		}, nil
	}

	pid := cmd.Process.Pid
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(pid)), 0o600); err != nil {
		// Couldn't persist the PID -- kill what we just started so we
		// don't leak a process no one can find.
		_ = terminatePID(pid)
		return nil, nil
	}
	// Detach so this hook's lifetime does not gate the child's lifetime.
	_ = cmd.Process.Release()

	if err := hudstate.Save(hudstate.State{
		Proxy: &hudstate.ProxyState{
			Port:     portFromListen(autoProxyListen),
			Upstream: upstream,
		},
		Retry: hudstate.RetryProxy,
		Mode:  e.PermissionMode,
	}); err != nil {
		// hudstate is observability; failure here must not block the
		// session start. The fork itself has already succeeded.
	}

	// We deliberately do NOT inject ANTHROPIC_BASE_URL ourselves -- that
	// would violate PLAN.md §9's "auto-inject env" rejection. The user
	// runs `export ANTHROPIC_BASE_URL=http://127.0.0.1:8080` (or sets
	// it in their shell rc) when they want traffic routed through us.
	return &payload.Response{
		SystemMessage: fmt.Sprintf(
			"auto-started 429 retry proxy on %s -> %s. To route traffic through it, set ANTHROPIC_BASE_URL=http://%s (chained upstream preserved for non-Anthropic endpoints).",
			autoProxyListen, upstream, autoProxyListen),
	}, nil
}

func autoProxyEnd(_ context.Context, _ *payload.Event) (*payload.Response, error) {
	pidPath, err := autoProxyPIDPath()
	if err != nil {
		return nil, nil
	}
	data, err := os.ReadFile(pidPath)
	if err != nil {
		// No PID file: nothing to do (no autoproxy session was active
		// this turn, or the file was already cleaned up). Leave hudstate
		// untouched.
		return nil, nil
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		_ = os.Remove(pidPath)
		return nil, nil
	}
	if err := terminatePID(pid); err != nil && !errors.Is(err, syscall.ESRCH) {
		// Process is gone (ESRCH) is fine -- the previous attempt at
		// cleanup already worked. Any other error we surface as a hint
		// but do not abort the hook.
		if err := hudstate.Save(hudstate.State{
			Retry: hudstate.RetryOff,
		}); err == nil {
			_ = os.Remove(pidPath)
		}
		return nil, nil
	}
	_ = os.Remove(pidPath)
	if err := hudstate.Save(hudstate.State{
		Retry: hudstate.RetryOff,
	}); err != nil {
		// Same as above: hudstate is observability.
	}
	return nil, nil
}

// autoProxyPIDPath is the PID file location, created with 0700 perms via the
// existing state-dir machinery in pkg/dir.
func autoProxyPIDPath() (string, error) {
	root, err := dir.Root()
	if err != nil {
		return "", err
	}
	state := filepath.Join(root, "state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(state, "autoproxy.pid"), nil
}

// refersToLocalProxy reports whether netProxyURL points at the listen
// address autoproxy itself would bind. Accepts any scheme that url.Parse
// recognises (http, https, socks5) so long as host:port match.
func refersToLocalProxy(netProxyURL, listen string) bool {
	wantHost, _, err := net.SplitHostPort(listen)
	if err != nil {
		wantHost = listen
	}
	u, err := url.Parse(netProxyURL)
	if err != nil {
		return false
	}
	return u.Hostname() == wantHost || u.Host == listen
}

// portFromListen extracts the numeric port from a listen address, returning
// 0 when none is parseable. Used by hudstate so the HUD can render a port
// number without reparsing the listen string.
func portFromListen(listen string) int {
	_, p, err := net.SplitHostPort(listen)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(p)
	if err != nil {
		return 0
	}
	return n
}
