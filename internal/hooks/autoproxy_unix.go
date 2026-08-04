//go:build !windows

package hooks

import (
	"errors"
	"os/exec"
	"syscall"
)

// startProxyChild builds the exec.Cmd for the proxy child and starts it.
// Kept as a package var so tests can substitute a recorder. The cmd's env
// is stripped of HTTPS_PROXY/HTTP_PROXY/ALL_PROXY so the child reaches its
// upstream directly even when the parent is configured to use a network
// proxy.
//
// Unix only: Setpgid puts the child in its own process group so the parent
// can signal the whole group on shutdown (defaultTerminatePID uses the
// negative-PID convention to target a process group).
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

// defaultTerminatePID sends SIGTERM to the process group rooted at pid, so
// any grandchildren are also signalled. Returns an error so callers can
// fall back to SIGKILL if needed.
func defaultTerminatePID(pid int) error {
	return syscall.Kill(-pid, syscall.SIGTERM)
}

// errProcessNotFound is the sentinel callers use to detect "the process is
// already gone", so a stale PID file does not surface as a hard error.
// On Unix, ESRCH from kill(2) is the canonical signal.
var errProcessNotFound = syscall.ESRCH

// ensure errors is referenced on the unix build (the import is shared with
// windows but used only here on this branch).
var _ = errors.New