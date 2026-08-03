package cmd

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/zealot00/claude-toolkit/internal/testloc"
)

// maxTestLines caps how much failing test output reaches Claude. The failure
// summary lives at the tail, so we keep the last maxTestLines lines and drop
// the banner noise in front.
const maxTestLines = 35

// testCmd runs the incremental tests covering a source file:
//
//	claude-toolkit test <file.go|file.py>
//
// Go: `go test -count=1 [-run ^(TestFoo|ExampleFoo|BenchmarkFoo)$] .` in the
// package directory. Python: `pytest <located test file> --maxfail=1
// --tb=short -q`.
//
// It is a standalone command -- deliberately NOT run from inside a hook, whose
// ~60s timeout would kill a real test run. The PostToolUse hook only detects
// that tests exist and tells Claude to call this command.
func testCmd(args []string) int {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	timeout := fs.Duration("timeout", 120*time.Second, "maximum time the test may take")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s test <file.go|file.py> [--timeout=<duration>]\n\n"+
			"Runs the incremental tests covering a source file (go test / pytest).\n"+
			"Failing output is truncated to %d lines.\n\nFlags:\n", binName, maxTestLines)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fs.Usage()
		return 2
	}

	res, err := testloc.Locate(rest[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	var cmd *exec.Cmd
	var label string
	switch res.Kind {
	case testloc.KindGo:
		args := []string{"test", "-count=1"}
		if res.TestPattern != "" {
			args = append(args, "-run", res.TestPattern)
		}
		args = append(args, ".")
		cmd = exec.CommandContext(ctx, "go", args...)
		cmd.Dir = res.Dir
		label = "go test"
	case testloc.KindPython:
		cmd = exec.CommandContext(ctx, "pytest", res.TestFile, "--maxfail=1", "--tb=short", "-q")
		cmd.Dir = filepath.Dir(res.TestFile)
		label = "pytest"
	}

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err = cmd.Run()
	text := tailLines(out.String(), maxTestLines)

	if err != nil {
		fmt.Fprintf(os.Stderr, "%s failed:\n%s\n", label, text)
		return 1
	}
	fmt.Println(text)
	return 0
}

// tailLines keeps the last n lines of s, prefixing an omission notice when it
// truncates. Failing test output has the useful part at the end.
func tailLines(s string, n int) string {
	trimmed := strings.TrimRight(s, "\n")
	if trimmed == "" {
		return "(no output)"
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) <= n {
		return trimmed
	}
	return fmt.Sprintf("... (%d lines omitted)\n%s", len(lines)-n, strings.Join(lines[len(lines)-n:], "\n"))
}
