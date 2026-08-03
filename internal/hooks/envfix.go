package hooks

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/zealot00/claude-toolkit/internal/dispatcher"
	"github.com/zealot00/claude-toolkit/internal/payload"
)

// EnvFix is the "envfix" capability. On PreToolUse it rewrites bare
// interpreter invocations (pytest, python, pip, node, ...) to the project's
// local virtual environment when one exists, via updatedInput -- instead of
// denying, it makes the command use .venv/bin/<tool>.
//
// It only rewrites BARE command names (no path), so /usr/bin/python and
// ./tools/pytest are left alone. Complex compound commands (pipes, redirects,
// &&) are also left alone: rewriting those is where mistakes happen.
//
// Caveat: some Claude Code versions (<= 2.1.220) silently drop updatedInput
// on Bash (#79321/#81340); the command then runs unmodified, which is the
// fail-open outcome.
func EnvFix() *dispatcher.Route {
	return &dispatcher.Route{
		Name:    "envfix",
		Events:  []string{payload.EventPreToolUse},
		Tools:   []string{"Bash"},
		Handler: envFix,
	}
}

// envFixTargets are the bare commands worth pinning to a local venv.
var envFixTargets = map[string]bool{
	"pytest": true, "python": true, "python3": true, "pip": true, "pip3": true,
	"node": true, "npm": true, "npx": true, "yarn": true, "pnpm": true, "bun": true,
}

func envFix(_ context.Context, e *payload.Event) (*payload.Response, error) {
	in, err := e.Bash()
	if err != nil || in.Command == "" || e.Cwd == "" {
		return nil, nil
	}

	// Only a bare invocation: `pytest [args]` with no shell metacharacters
	// anywhere in the line. Pipes, redirects, && and $(...) are too risky to
	// rewrite.
	if strings.ContainsAny(in.Command, "|&;<>`$") {
		return nil, nil
	}
	fields := strings.Fields(in.Command)
	if len(fields) == 0 {
		return nil, nil
	}
	base := filepath.Base(fields[0])
	if strings.Contains(fields[0], "/") || !envFixTargets[base] {
		return nil, nil // absolute/relative path, or not a venv target
	}

	venvBin := filepath.Join(e.Cwd, ".venv", "bin", base)
	if _, err := os.Stat(venvBin); err != nil {
		return nil, nil // no local venv for this tool
	}

	newCmd := venvBin
	if len(fields) > 1 {
		newCmd += " " + strings.Join(fields[1:], " ")
	}
	return payload.AllowWithRewrite("rewritten to use the project's local virtual environment", payload.BashInput{
		Command:         newCmd,
		Description:     in.Description,
		Timeout:         in.Timeout,
		RunInBackground: in.RunInBackground,
	}), nil
}
