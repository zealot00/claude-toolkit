package cmd

import (
	"sort"

	"github.com/zealot00/claude-toolkit/internal/capcfg"
	"github.com/zealot00/claude-toolkit/internal/dispatcher"
	"github.com/zealot00/claude-toolkit/internal/hooks"
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

// enabledCapabilities reports each capability's effective state: an explicit
// false in capcfg disables it; missing or true enables it (the fail-open
// default). This keeps `manage list` consistent with what the hook runtime
// actually runs.
func enabledCapabilities() (map[string]bool, error) {
	cfg, err := capcfg.Load()
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, c := range registeredCapabilities() {
		on := true
		if v, ok := cfg.Enabled[c.name]; ok {
			on = v
		}
		out[c.name] = on
	}
	return out, nil
}
