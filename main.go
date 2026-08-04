// Command claude-toolkit is a self-bootstrapping hook runner for Claude Code.
//
// It ships as a single static binary. `claude-toolkit init` registers itself
// into ~/.claude/settings.json; Claude Code then pipes hook events to
// `claude-toolkit run` on stdin as JSON.
package main

import (
	"os"

	"github.com/zealot00/claude-toolkit/cmd"
)

func main() {
	cmd.SetPluginAssets(pluginAssets)
	os.Exit(cmd.Execute(os.Args[1:]))
}
