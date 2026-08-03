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
// the project's formatter (and, where cheap, a fixer) over a file Claude just
// wrote.
//
// The point is not tidiness. When a formatter rewrites a file, Claude's
// in-context copy silently goes stale, and its next Edit fails on a string
// that no longer matches. So the hook reports back only when the file actually
// changed, telling Claude to re-read before editing again.
//
// Every tool in the pipeline is optional: a missing binary is skipped, so the
// pipeline degrades (goimports -> gofmt; ruff check -> ruff format -> black;
// -> nothing) instead of failing the session.
func Formatter() *dispatcher.Route {
	return &dispatcher.Route{
		Name:    "format",
		Events:  []string{payload.EventPostToolUse},
		Tools:   []string{"Write", "Edit", "NotebookEdit"},
		Handler: format,
	}
}

// formatterStep is one optional command in a file's formatting pipeline.
type formatterStep struct {
	name string
	args []string
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

	steps, ok := formatterFor(path)
	if !ok {
		return nil, nil
	}

	for _, st := range steps {
		cmd := exec.CommandContext(ctx, st.name, append(st.args, path)...)
		cmd.Dir = filepath.Dir(path)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			// A fixer/formatter that fails usually means the file does not
			// parse. That is worth telling Claude about -- it just wrote
			// broken syntax.
			msg := strings.TrimSpace(stderr.String())
			if msg == "" {
				return nil, nil
			}
			return payload.Context(payload.EventPostToolUse, fmt.Sprintf(
				"`%s` failed on %s, which usually means the file no longer parses:\n\n%s",
				st.name, filepath.Base(path), truncate(msg, 1500))), nil
		}
	}

	after, err := os.ReadFile(path)
	if err != nil || bytes.Equal(before, after) {
		return nil, nil
	}
	return payload.Context(payload.EventPostToolUse, fmt.Sprintf(
		"`%s` reformatted %s on disk. Your copy of this file is now stale -- re-read it before your next edit.",
		steps[0].name, path)), nil
}

// formatterFor returns the ordered formatting pipeline for a file's
// extension, ok=false when no tool for it is installed.
func formatterFor(path string) ([]formatterStep, bool) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		// goimports fixes missing/unordered imports before gofmt normalises
		// layout; gofmt alone only formats.
		if n, a, ok := lookup("goimports", "-w"); ok {
			return []formatterStep{{n, a}}, true
		}
		return lookupStep("gofmt", "-w")
	case ".rs":
		return lookupStep("rustfmt", "--edition", "2021")
	case ".sh", ".bash":
		return lookupStep("shfmt", "-w")
	case ".py":
		// Fix unused imports / undefined names first, then format. ruff is
		// preferred for both; black is the fallback formatter only.
		var steps []formatterStep
		if n, a, ok := lookup("ruff", "check", "--fix", "--select", "F401,F821", "--quiet"); ok {
			steps = append(steps, formatterStep{n, a})
		}
		if n, a, ok := lookup("ruff", "format", "--quiet"); ok {
			steps = append(steps, formatterStep{n, a})
		}
		if len(steps) > 0 {
			return steps, true
		}
		return lookupStep("black", "-q")
	case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".json", ".css", ".scss", ".less", ".html", ".vue", ".svelte", ".md", ".yaml", ".yml":
		return lookupStepIn(filepath.Dir(path), "prettier", "--write", "--log-level", "warn")
	}
	return nil, false
}

func lookupStep(name string, args ...string) ([]formatterStep, bool) {
	if n, a, ok := lookup(name, args...); ok {
		return []formatterStep{{n, a}}, true
	}
	return nil, false
}

func lookupStepIn(dir, name string, args ...string) ([]formatterStep, bool) {
	if n, a, ok := lookupIn(dir, name, args...); ok {
		return []formatterStep{{n, a}}, true
	}
	return nil, false
}

// lookPath is exec.LookPath, indirected so tests can stub tool presence.
var lookPath = exec.LookPath

func lookup(name string, args ...string) (string, []string, bool) {
	p, err := lookPath(name)
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
