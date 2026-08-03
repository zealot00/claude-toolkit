package hooks

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/zealot00/claude-toolkit/internal/dispatcher"
	"github.com/zealot00/claude-toolkit/internal/payload"
)

// SessionContext is the "enrich" capability. It injects the repository's
// current state into the conversation at session start so Claude opens every
// session knowing which branch it is on and what is already uncommitted,
// instead of wasting its first tool calls rediscovering that.
//
// The capability may eventually fire on UserPromptSubmit as well; routing is
// declared by the Events field so adding a second event does not require a
// second registration.
func SessionContext() *dispatcher.Route {
	return &dispatcher.Route{
		Name:    "enrich",
		Events:  []string{payload.EventSessionStart},
		Handler: sessionContext,
	}
}

func sessionContext(ctx context.Context, e *payload.Event) (*payload.Response, error) {
	dir := e.Cwd
	if dir == "" {
		return nil, nil
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

	return payload.Context(payload.EventSessionStart, strings.TrimRight(b.String(), "\n")), nil
}

// maxStatusLines caps the injected file list. Hook output is truncated at
// 10,000 characters, and a 400-file diff is noise rather than context.
const maxStatusLines = 15

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
