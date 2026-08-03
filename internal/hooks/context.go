package hooks

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/zealot00/claude-toolkit/internal/dispatcher"
	"github.com/zealot00/claude-toolkit/internal/payload"
)

// SessionContext is the "enrich" capability. On SessionStart it injects the
// repository's current state (branch, divergence, dirty files, recent
// commits, toolchain) into the conversation so Claude opens every session
// knowing where it is. On UserPromptSubmit it re-injects a one-line cwd/dirty
// reminder, so a long session notices when the working tree drifts.
//
// Everything here is read-only and failure-tolerant: missing git or toolchain
// simply means less context.
func SessionContext() *dispatcher.Route {
	return &dispatcher.Route{
		Name:    "enrich",
		Events:  []string{payload.EventSessionStart, payload.EventUserPromptSubmit},
		Handler: sessionContext,
	}
}

const (
	// maxContextLen caps the injected context. Claude Code truncates hook
	// output at 10,000 characters; truncating here keeps the text complete
	// rather than silently cut mid-line by the host.
	maxContextLen = 10000
	// minRemaining is the budget a SessionStart hook needs to gather git
	// context. Below it the hook degrades to a no-op rather than let the
	// session wait on a hook that cannot finish.
	minRemaining = 3 * time.Second
	// maxStatusLines caps the injected file list. A 400-file diff is noise
	// rather than context.
	maxStatusLines = 15
)

func sessionContext(ctx context.Context, e *payload.Event) (*payload.Response, error) {
	dir := e.Cwd
	if dir == "" {
		return nil, nil
	}
	if left, ok := dispatcher.Remaining(ctx); ok && left < minRemaining {
		return nil, nil // not enough time; missing context beats a hung session
	}

	// UserPromptSubmit gets a one-line reminder, not the full dump.
	if e.HookEventName == payload.EventUserPromptSubmit {
		line := fmt.Sprintf("## cwd: %s", dir)
		if status := git(ctx, dir, "status", "--porcelain"); status != "" {
			line += fmt.Sprintf(" | %d uncommitted file(s)", len(strings.Split(status, "\n")))
		}
		return payload.Context(e.HookEventName, line), nil
	}

	if !inGitRepo(ctx, dir) {
		return nil, nil
	}

	var b strings.Builder
	b.WriteString("## Repository state (claude-toolkit)\n\n")

	if branch := git(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD"); branch != "" {
		fmt.Fprintf(&b, "- Branch: `%s`", branch)
		if ab := aheadBehind(ctx, dir); ab != "" {
			fmt.Fprintf(&b, " (%s)", ab)
		}
		b.WriteString("\n")
	}

	status := git(ctx, dir, "status", "--porcelain")
	if status == "" {
		b.WriteString("- Working tree: clean\n")
	} else {
		lines := strings.Split(status, "\n")
		fmt.Fprintf(&b, "- Working tree: %d file(s) modified\n", len(lines))
		for i, l := range lines {
			if i == maxStatusLines {
				fmt.Fprintf(&b, "  - ... and %d more\n", len(lines)-maxStatusLines)
				break
			}
			fmt.Fprintf(&b, "  - %s\n", strings.TrimSpace(l))
		}
	}

	if log := git(ctx, dir, "log", "-5", "--pretty=format:%h %s"); log != "" {
		b.WriteString("- Recent commits:\n")
		for l := range strings.SplitSeq(log, "\n") {
			fmt.Fprintf(&b, "  - %s\n", l)
		}
	}

	if tc := toolchainLine(ctx); tc != "" {
		b.WriteString(tc)
	}

	out := strings.TrimRight(b.String(), "\n")
	return payload.Context(payload.EventSessionStart, truncateContext(out, maxContextLen)), nil
}

// toolchainLine reports the active Go/Python/Node toolchain and venv, or ""
// when none is detectable. Version probes are best-effort and skipped when
// the hook is running low on time.
func toolchainLine(ctx context.Context) string {
	if left, ok := dispatcher.Remaining(ctx); ok && left < 5*time.Second {
		return "" // not enough budget left for three version probes
	}
	var parts []string
	if v := toolVersion(ctx, "go", "version"); v != "" {
		parts = append(parts, "Go "+v)
	}
	if v := toolVersion(ctx, "python3", "--version"); v != "" {
		parts = append(parts, "Python "+v)
	} else if v := toolVersion(ctx, "python", "--version"); v != "" {
		parts = append(parts, "Python "+v)
	}
	if v := toolVersion(ctx, "node", "--version"); v != "" {
		parts = append(parts, "Node "+v)
	}
	if venv := os.Getenv("VIRTUAL_ENV"); venv != "" {
		parts = append(parts, "venv "+venv)
	}
	if len(parts) == 0 {
		return ""
	}
	return "- Toolchain: " + strings.Join(parts, ", ") + "\n"
}

// toolVersion runs a version probe and returns the first line, or "".
func toolVersion(ctx context.Context, name string, args ...string) string {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	// go version prints "go version go1.25.11 ..."; python --version writes
	// to stderr with a "Python " prefix. Strip both so the line is the bare
	// version.
	line = strings.TrimPrefix(line, "go version ")
	if v, ok := strings.CutPrefix(line, "Python "); ok {
		return v
	}
	return line
}

// truncateContext trims s to limit bytes, appending an ellipsis so the
// truncation is visible rather than a silent mid-word cut.
func truncateContext(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "\n...(truncated by claude-toolkit)"
}

func inGitRepo(ctx context.Context, dir string) bool {
	return git(ctx, dir, "rev-parse", "--is-inside-work-tree") == "true"
}

// aheadBehind renders the divergence from the upstream branch, or "" when
// there is no upstream configured.
func aheadBehind(ctx context.Context, dir string) string {
	out := git(ctx, dir, "rev-list", "--left-right", "--count", "@{upstream}...HEAD")
	f := strings.Fields(out)
	if len(f) != 2 {
		return ""
	}
	behind, err1 := strconv.Atoi(f[0])
	ahead, err2 := strconv.Atoi(f[1])
	if err1 != nil || err2 != nil || (behind == 0 && ahead == 0) {
		return ""
	}
	switch {
	case behind == 0:
		return fmt.Sprintf("%d ahead", ahead)
	case ahead == 0:
		return fmt.Sprintf("%d behind", behind)
	default:
		return fmt.Sprintf("%d ahead, %d behind", ahead, behind)
	}
}

// git runs a git command and returns trimmed stdout, or "" on any failure.
// Failure is never fatal here: missing context is better than a broken session.
func git(ctx context.Context, dir string, args ...string) string {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
