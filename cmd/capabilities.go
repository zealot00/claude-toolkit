package cmd

import (
	"sort"

	"github.com/zealot00/claude-toolkit/internal/dispatcher"
	"github.com/zealot00/claude-toolkit/internal/hooks"
	"github.com/zealot00/claude-toolkit/pkg/installer"
)

// capability describes one registered capability for the management UI. A
// capability ("guard", "format", "enrich") is one dispatcher.Route Name; it
// may listen on several events (loopguard records on PostToolUse and blocks
// on PreToolUse), and EVERY event needs its own settings.json matcher group,
// otherwise that half of the capability never fires.
type capability struct {
	name    string   // e.g. "guard"
	events  []string // all hook events this capability listens on
	matcher string   // settings.json matcher covering its tools (same for all events)
}

// registeredCapabilities returns the toolkit's capabilities, one per route
// Name, sorted for stable output in manage and init.
func registeredCapabilities() []capability {
	d := hooks.Register()
	byName := map[string]*dispatcher.Route{}
	var names []string
	for _, r := range d.Routes() {
		if _, dup := byName[r.Name]; dup {
			continue
		}
		byName[r.Name] = r
		names = append(names, r.Name)
	}
	sort.Strings(names)

	out := make([]capability, 0, len(names))
	for _, n := range names {
		r := byName[n]
		out = append(out, capability{name: r.Name, events: r.Events, matcher: r.Matcher()})
	}
	return out
}

// enabledCapabilities reports which capability names are currently installed
// in the given settings file, per installer.Inspect. A legacy entry without a
// --cap flag contributes nothing; manage refuses to write while any exist
// rather than silently stripping them (see legacyCapabilities).
func enabledCapabilities(path string) (map[string]bool, error) {
	entries, err := installer.Inspect(path)
	if err != nil {
		return nil, err
	}
	enabled := map[string]bool{}
	for _, e := range entries {
		if e.Capability != "" {
			enabled[e.Capability] = true
		}
	}
	return enabled, nil
}

// legacyCapabilities counts installed entries that predate capability tags
// (commands without --cap=). manage cannot attribute them to a capability, so
// any write would strip them and re-add only what was requested; refusing
// until the user runs `init` (which migrates) is the safe move.
func legacyCapabilities(path string) (int, error) {
	entries, err := installer.Inspect(path)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if e.Capability == "" {
			n++
		}
	}
	return n, nil
}
