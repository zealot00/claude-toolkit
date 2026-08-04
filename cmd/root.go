package cmd

import (
	"embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/zealot00/claude-toolkit/internal/payload"
)

// Build metadata, injected via -ldflags at release time. See the Makefile.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// pluginAssetsFS holds the embedded plugin payload (.claude-plugin/,
// commands/, hooks/) so that `init --scope=skills-*` can install it without
// depending on the source tree being next to the binary. main() wires this
// up at startup.
var pluginAssetsFS embed.FS

// SetPluginAssets installs the embedded plugin payload. Called once from
// main(); subsequent calls panic because the FS is meant to be set before
// any command runs.
func SetPluginAssets(fs embed.FS) { pluginAssetsFS = fs }

// PluginAssets returns the embedded plugin payload. Tests may call it.
func PluginAssets() embed.FS { return pluginAssetsFS }

// binName is what the installed executable is expected to be called on PATH.
const binName = "claude-toolkit"

// eventAlias maps canonical hook event names to the short form used in
// `--event=`. The alias exists so the generated settings.json reads clearly;
// the authoritative event always comes from the stdin payload.
var eventAlias = map[string]string{
	payload.EventPreToolUse:         "pre",
	payload.EventPostToolUse:        "post",
	payload.EventPostToolUseFailure: "failure",
	payload.EventSessionStart:       "session",
	payload.EventSessionEnd:         "session-end",
	payload.EventUserPromptSubmit:   "prompt",
	payload.EventStop:               "stop",
}

// canonicalEvent resolves an alias or a canonical name to a canonical name.
func canonicalEvent(s string) (string, bool) {
	for canon, alias := range eventAlias {
		if s == alias || s == canon {
			return canon, true
		}
	}
	return "", false
}

// Execute runs the CLI and returns the process exit code.
func Execute(args []string) int {
	if len(args) == 0 {
		usage(os.Stderr)
		return 2
	}
	switch args[0] {
	case "run":
		return runCmd(args[1:])
	case "init":
		return initCmd(args[1:])
	case "manage":
		return manageCmd(args[1:])
	case "test":
		return testCmd(args[1:])
	case "ast":
		return astCmd(args[1:])
	case "rules":
		return rulesCmd(args[1:])
	case "proxy":
		return proxyCmd(args[1:])
	case "upgrade":
		return upgradeCmd(args[1:])
	case "uninstall":
		return uninstallCmd(args[1:])
	case "log":
		return logCmd(args[1:])
	case "doctor":
		return doctorCmd(args[1:])
	case "version", "--version", "-v":
		fmt.Printf("%s %s (commit %s, built %s)\n", binName, Version, Commit, Date)
		return 0
	case "help", "--help", "-h":
		usage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "%s: unknown command %q\n\n", binName, args[0])
		usage(os.Stderr)
		return 2
	}
}

func usage(w io.Writer) {
	fmt.Fprintf(w, `%s %s -- a self-bootstrapping hook runner for Claude Code.

Usage:
  %s <command> [flags]

Commands:
  init      Register the toolkit's hooks in ~/.claude/settings.json
  manage    List, enable or disable hook capabilities (also via /toolkit plugin)
  test      Run incremental tests covering a source file (go test / pytest)
  ast       Print a compressed structural summary of a .go/.py file
  rules     List every built-in rule and its verdict
  proxy     Run the optional local API proxy (429 auto-retry; opt-in)
  upgrade   Check for and install a newer release
  uninstall Remove the toolkit's hooks (--purge-config also deletes state)
  log       Tail the debug log written when CLAUDE_TOOLKIT_DEBUG is set
  doctor    Diagnose the installation and self-test every hook
  run       Execute a hook; reads the event JSON on stdin (invoked by Claude Code)
  version   Print version information

Getting started:
  go install github.com/zealot00/claude-toolkit@latest
  %s init
  %s doctor

Run "%s <command> --help" for the flags of a specific command.
`, binName, Version, binName, binName, binName, binName)
}

// debugf writes to the toolkit's log file when CLAUDE_TOOLKIT_DEBUG is set.
// Hook stdout is reserved for the JSON response, so diagnostics cannot go
// there, and stderr is invisible unless Claude Code runs with --debug.
func debugf(format string, args ...any) {
	if os.Getenv("CLAUDE_TOOLKIT_DEBUG") == "" {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(home, ".claude", "claude-toolkit.log"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(f, "%s\n", strings.TrimRight(msg, "\n"))
}
