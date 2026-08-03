package hooks

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/zealot00/claude-toolkit/internal/dispatcher"
	"github.com/zealot00/claude-toolkit/internal/payload"
)

// Formatter is the "format" capability. It is a PostToolUse hook that runs
// the project's formatter over a file Claude just wrote.
//
// The point is not tidiness. When a formatter rewrites a file, Claude's
// in-context copy silently goes stale, and its next Edit fails on a string
// that no longer matches. So the hook reports back only when the file actually
// changed, telling Claude to re-read before editing again.
//
// "format" will eventually be folded into the broader "heal" capability,
// which will also run goimports / ruff --fix and incremental tests; the two
// share the same PostToolUse slot.
func Formatter() *dispatcher.Route {
	return &dispatcher.Route{
		Name:    "format",
		Events:  []string{payload.EventPostToolUse},
		Tools:   []string{"Write", "Edit", "NotebookEdit"},
		Handler: format,
	}
}

func format(ctx context.Context, e *payload.Event) (*payload.Response, error) {
	in, err := e.File()
	if err != nil {
		return nil, err
	}
	path := in.Path()
	if path == "" {
		return nil, nil
	}

	before, err := os.ReadFile(path)
	if err != nil {
		// The file may have been deleted or moved by the tool. Not our problem.
		return nil, nil
	}

	name, args, ok := formatterFor(path)
	if !ok {
		return nil, nil
	}

	cmd := exec.CommandContext(ctx, name, append(args, path)...)
	cmd.Dir = filepath.Dir(path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// A formatter that fails usually means the file does not parse. That
		// is worth telling Claude about -- it just wrote broken syntax.
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return nil, nil
		}
		return payload.Context(payload.EventPostToolUse, fmt.Sprintf(
			"`%s` failed on %s, which usually means the file no longer parses:\n\n%s",
			name, filepath.Base(path), truncate(msg, 1500))), nil
	}

	after, err := os.ReadFile(path)
	if err != nil || bytes.Equal(before, after) {
		return nil, nil
	}
	return payload.Context(payload.EventPostToolUse, fmt.Sprintf(
		"`%s` reformatted %s on disk. Your copy of this file is now stale -- re-read it before your next edit.",
		name, path)), nil
}

// formatterFor picks a formatter by extension, returning ok=false when none is
// configured or the tool is not installed.
func formatterFor(path string) (name string, args []string, ok bool) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return lookup("gofmt", "-w")
	case ".rs":
		return lookup("rustfmt", "--edition", "2021")
	case ".sh", ".bash":
		return lookup("shfmt", "-w")
	case ".py":
		if n, a, ok := lookupIn(filepath.Dir(path), "ruff", "format"); ok {
			return n, a, true
		}
		return lookup("black", "-q")
	case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".json", ".css", ".scss", ".less", ".html", ".vue", ".svelte", ".md", ".yaml", ".yml":
		return lookupIn(filepath.Dir(path), "prettier", "--write", "--log-level", "warn")
	}
	return "", nil, false
}

func lookup(name string, args ...string) (string, []string, bool) {
	p, err := exec.LookPath(name)
	if err != nil {
		return "", nil, false
	}
	return p, args, true
}

// lookupIn prefers a project-local node_modules/.bin (or equivalent) copy over
// whatever happens to be on PATH, so the file is formatted with the version the
// project actually pins.
func lookupIn(dir, name string, args ...string) (string, []string, bool) {
	for d := dir; ; {
		candidate := filepath.Join(d, "node_modules", ".bin", name)
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate, args, true
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	return lookup(name, args...)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n... (truncated)"
}
