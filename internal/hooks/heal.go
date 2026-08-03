package hooks

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zealot00/claude-toolkit/internal/dispatcher"
	"github.com/zealot00/claude-toolkit/internal/payload"
	"github.com/zealot00/claude-toolkit/internal/testloc"
)

// Heal is the "heal" capability. On PostToolUse it detects whether
// incremental tests cover the file Claude just wrote and, if so, tells Claude
// to run them via the standalone `claude-toolkit test` command.
//
// It deliberately does NOT run the tests itself: a hook's ~60s timeout would
// kill a real test run. Detection only, then a one-line pointer.
func Heal() *dispatcher.Route {
	return &dispatcher.Route{
		Name:    "heal",
		Events:  []string{payload.EventPostToolUse},
		Tools:   []string{"Write", "Edit", "NotebookEdit"},
		Handler: heal,
	}
}

func heal(_ context.Context, e *payload.Event) (*payload.Response, error) {
	in, err := e.File()
	if err != nil {
		return nil, err
	}
	path := in.Path()
	if path == "" {
		return nil, nil
	}

	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		if !hasGoTests(filepath.Dir(path)) {
			return nil, nil
		}
	case ".py":
		if _, err := testloc.Locate(path); err != nil {
			return nil, nil // no pytest target exists
		}
	default:
		return nil, nil
	}

	return payload.Context(payload.EventPostToolUse, fmt.Sprintf(
		"Incremental tests cover %s. Run `claude-toolkit test %s` to verify this change (output truncated to the last 35 lines).",
		path, path)), nil
}

// hasGoTests reports whether the directory holds any *_test.go file.
func hasGoTests(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), "_test.go") {
			return true
		}
	}
	return false
}
