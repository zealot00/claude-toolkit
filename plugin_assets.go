// Package main is the claude-toolkit binary. plugin_assets.go bundles the
// Claude Code plugin payload (manifest, slash command, hooks/hooks.json) into
// the binary so that `claude-toolkit init --scope=skills-*` can copy them out
// without depending on the source tree being next to the installed binary.
//
// `go install` puts the binary in ~/go/bin, far from the repo, so a runtime
// filesystem lookup is unreliable. Embedding turns the binary into a single
// self-contained artifact that always knows where its plugin files are.
package main

import "embed"

//go:embed .claude-plugin
//go:embed commands
//go:embed hooks
var pluginAssets embed.FS
