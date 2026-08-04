package hooks

import "github.com/zealot00/claude-toolkit/internal/dispatcher"

// Register wires every hook this toolkit ships into a dispatcher.
//
// This is the single source of truth for what the toolkit does. `init` derives
// the settings.json entries from these routes and `doctor` verifies them
// against the same list, so the installed configuration cannot drift from the
// compiled behaviour.
func Register() *dispatcher.Dispatcher {
	d := dispatcher.New()
	d.Register(Guard())
	d.Register(SessionContext())
	d.Register(Formatter())
	d.Register(Heal())
	d.Register(LoopGuard())
	d.Register(Notifier())
	d.Register(EnvFix())
	d.Register(Autoproxy())
	d.Register(RetryGuard())
	return d
}
