package cmd

import (
	"fmt"
	"os"
)

// pluginHintValue is the marketplace-qualified plugin identifier that
// Claude Code's hint protocol expects in the `value` attribute. The format
// is "<plugin-name>@<marketplace-name>"; the marketplace is the one a user
// adds with `/plugin marketplace add`. Until the marketplace entry ships,
// this string is treated as a placeholder by Claude Code -- it is still
// emitted so the protocol path is fully wired and the docs are correct.
const pluginHintValue = "claude-toolkit@claude-toolkit-dev"

// hookSubcommand is the subcommand Claude Code itself invokes for every
// hook event. Suppressing the hint on this path keeps the per-event
// invocation silent; the hint only fires on user-facing commands.
const hookSubcommand = "run"

// maybeEmitHint writes a single <claude-code-hint /> tag to stderr when
// claude-toolkit runs inside a Claude Code session and the invoked command
// is not the hook entrypoint. Claude Code strips the tag (zero token cost)
// and, if it points at a plugin in an Anthropic-controlled marketplace
// that the user has not yet installed or dismissed, surfaces a one-time
// install prompt.
//
// Emit rules, matching /docs/en/plugin-hints:
//
//   - CLAUDECODE=1 means we are inside Claude Code's hook runtime; that
//     includes the hook invocation itself, which is why hookSubcommand is
//     excluded below.
//   - Claude Code deduplicates hints across invocations in a single
//     session, so emitting on every user-facing command is harmless.
//   - We never emit outside Claude Code, where the tag would just be
//     noise on a developer terminal.
//
// The output goes to stderr, not stdout, because hook commands reserve
// stdout for the JSON response document.
func maybeEmitHint() {
	if !runningInsideClaudeCode() {
		return
	}
	if len(os.Args) > 1 && os.Args[1] == hookSubcommand {
		return
	}
	fmt.Fprintf(os.Stderr, `<claude-code-hint v="1" type="plugin" value=%q />`+"\n", pluginHintValue)
}

// runningInsideClaudeCode reports whether the current process is a
// descendant of a Claude Code session. Claude Code sets CLAUDECODE=1 on
// every child process it spawns; CLAUDE_CODE_ENTRYPOINT is a more recent
// signal that some hosts use instead. Either is enough.
func runningInsideClaudeCode() bool {
	return os.Getenv("CLAUDECODE") == "1" || os.Getenv("CLAUDE_CODE_ENTRYPOINT") != ""
}
