//go:build windows

package hooks

import (
	"errors"
	"os"
	"os/exec"
)

// startProxyChild builds the exec.Cmd for the proxy child and starts it.
// Kept as a package var so tests can substitute a recorder. The cmd's env
// is stripped of HTTPS_PROXY/HTTP_PROXY/ALL_PROXY so the child reaches its
// upstream directly even when the parent is configured to use a network
// proxy.
//
// Windows has no Setpgid equivalent. The closest portable semantics are
// CREATE_NEW_PROCESS_GROUP (declared in syscall.SysProcAttr.CreationFlags)
// which detaches the child from the parent's Ctrl+C group; we do not set
// it because Claude Code's hook runtime never relies on a shared Ctrl+C
// handler. The SessionEnd kill below falls back to TerminateProcess via
// os.Process.Kill, which is sufficient for a child we ourselves spawned.
func startProxyChild(self, listen, upstream string) (*exec.Cmd, error) {
	cmd := exec.Command(self, "proxy",
		"--listen="+listen,
		"--upstream="+upstream,
	)
	cmd.Env = childEnv()
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

// defaultTerminatePID sends a hard-kill to the child pid. Windows has no
// SIGTERM / process-group primitive, so we go straight to TerminateProcess
// (os.Process.Kill on Windows maps to it). The caller treats any "process
// not found" error as a no-op so a stale PID file is not surfaced as a
// hard error.
func defaultTerminatePID(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		// os.FindProcess on Windows never returns an error for a numeric
		// PID (the handle is lazy), so this branch is unreachable in
		// practice. Treat it as "process not found" anyway so callers can
		// rely on errors.Is(err, errProcessNotFound).
		return errProcessNotFound
	}
	if err := p.Kill(); err != nil {
		// ECHILD-equivalent: process is already gone.
		if errors.Is(err, os.ErrProcessDone) {
			return errProcessNotFound
		}
		return err
	}
	return nil
}

// errProcessNotFound is the sentinel callers use to detect "the process is
// already gone", so a stale PID file does not surface as a hard error.
var errProcessNotFound = errors.New("hooks: process not found")