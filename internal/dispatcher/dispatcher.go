// Package dispatcher routes a decoded hook event to the handlers registered
// for it. Routing is a map lookup plus a linear scan of that event's routes --
// no reflection, no allocation beyond the response itself -- because `run` sits
// on the hot path of every tool call Claude Code makes.
package dispatcher

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/zealot00/claude-toolkit/internal/payload"
)

// Handler inspects an event and optionally returns a response. Returning
// (nil, nil) means "no opinion" and produces no stdout.
type Handler func(ctx context.Context, e *payload.Event) (*payload.Response, error)

// Route binds a handler to one or more events, optionally narrowed to certain
// tools. A capability ("guard", "heal", "enrich") is one Route that may listen
// to several events -- the same enrich handler, for instance, can fire on
// SessionStart and UserPromptSubmit without registering twice.
type Route struct {
	// Name identifies the capability in diagnostics and doctor output, e.g.
	// "guard" / "heal" / "enrich" / "truncate".
	Name string
	// Events is the set of hook event names this route responds to, e.g.
	// [payload.EventPreToolUse] or
	// [payload.EventSessionStart, payload.EventUserPromptSubmit].
	Events []string
	// Tools narrows the route to specific tool names. Empty matches every
	// tool. Entries are matched literally, or as a regexp when they contain
	// regexp metacharacters.
	Tools []string
	// Handler runs when the route matches.
	Handler Handler

	toolRe []*regexp.Regexp
}

// Dispatcher holds the registered routes, keyed by event.
type Dispatcher struct {
	routes map[string][]*Route
}

// New returns an empty Dispatcher.
func New() *Dispatcher {
	return &Dispatcher{routes: make(map[string][]*Route)}
}

var metaChars = regexp.MustCompile(`[.*+?()\[\]{}^$\\]`)

// Register adds a route under each of its events. It panics on an invalid
// tool pattern, which can only happen from a programming error at wiring time,
// never from hook input.
func (d *Dispatcher) Register(r *Route) {
	if len(r.Events) == 0 {
		panic(fmt.Sprintf("dispatcher: route %q has no events", r.Name))
	}
	for _, t := range r.Tools {
		if !metaChars.MatchString(t) {
			r.toolRe = append(r.toolRe, nil) // literal; placeholder keeps indices aligned
			continue
		}
		re, err := regexp.Compile(t)
		if err != nil {
			panic(fmt.Sprintf("dispatcher: route %q: bad tool pattern %q: %v", r.Name, t, err))
		}
		r.toolRe = append(r.toolRe, re)
	}
	for _, ev := range r.Events {
		d.routes[ev] = append(d.routes[ev], r)
	}
}

// Routes returns every registered route, for diagnostics. The same route may
// appear multiple times if it is registered for several events.
func (d *Dispatcher) Routes() []*Route {
	var all []*Route
	for _, rs := range d.routes {
		all = append(all, rs...)
	}
	return all
}

// Events returns the distinct event names that have at least one route. The
// installer uses this to decide what to write into settings.json, so the
// config can never drift from the code.
func (d *Dispatcher) Events() []string {
	out := make([]string, 0, len(d.routes))
	for ev := range d.routes {
		out = append(out, ev)
	}
	sort.Strings(out)
	return out
}

// Matcher returns the settings.json matcher covering every route registered
// for event: the union of their tool patterns, anchored so substring matches
// (Claude Code uses unanchored JS RegExp.test) cannot leak through. A route
// that matches every tool collapses to "*".
func (d *Dispatcher) Matcher(event string) string {
	var tools []string
	seen := map[string]bool{}
	for _, r := range d.routes[event] {
		if len(r.Tools) == 0 {
			return "*"
		}
		for _, t := range r.Tools {
			if !seen[t] {
				seen[t] = true
				tools = append(tools, t)
			}
		}
	}
	if len(tools) == 0 {
		return "*"
	}
	return "^(" + strings.Join(tools, "|") + ")$"
}

func (r *Route) matchesTool(tool string) bool {
	if len(r.Tools) == 0 {
		return true
	}
	for i, t := range r.Tools {
		if re := r.toolRe[i]; re != nil {
			if re.MatchString(tool) {
				return true
			}
			continue
		}
		if t == tool {
			return true
		}
	}
	return false
}

// Dispatch runs every matching handler and merges their responses.
//
// Handler errors are collected rather than aborting the run: one broken hook
// must not suppress the verdict of a working one. The merged response and the
// joined error are both returned; callers decide how loud to be about the error.
func (d *Dispatcher) Dispatch(ctx context.Context, e *payload.Event) (*payload.Response, error) {
	var (
		merged *payload.Response
		errs   []string
	)
	for _, r := range d.routes[e.HookEventName] {
		if !r.matchesTool(e.ToolName) {
			continue
		}
		resp, err := r.Handler(ctx, e)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", r.Name, err))
			continue
		}
		merged = merge(merged, resp)
	}
	if len(errs) > 0 {
		return merged, fmt.Errorf("dispatcher: %s", strings.Join(errs, "; "))
	}
	return merged, nil
}

// merge folds b into a. Claude Code accepts one JSON document per hook process,
// so several matching handlers have to agree on a single answer.
//
// Precedence is deliberately safety-first: deny beats ask beats allow, and the
// first decision at a given severity wins. Context strings accumulate.
func merge(a, b *payload.Response) *payload.Response {
	if b == nil {
		return a
	}
	if a == nil {
		return b
	}
	if b.Decision != "" && a.Decision == "" {
		a.Decision, a.Reason = b.Decision, b.Reason
	}
	if b.SystemMessage != "" {
		a.SystemMessage = joinNonEmpty(a.SystemMessage, b.SystemMessage)
	}
	if b.Continue != nil && a.Continue == nil {
		a.Continue = b.Continue
		if a.StopReason == "" {
			a.StopReason = b.StopReason
		}
	}
	if b.HookSpecificOutput == nil {
		return a
	}
	if a.HookSpecificOutput == nil {
		a.HookSpecificOutput = b.HookSpecificOutput
		return a
	}
	ah, bh := a.HookSpecificOutput, b.HookSpecificOutput
	if severity(bh.PermissionDecision) > severity(ah.PermissionDecision) {
		ah.PermissionDecision = bh.PermissionDecision
		ah.PermissionDecisionReason = bh.PermissionDecisionReason
	}
	ah.AdditionalContext = joinNonEmpty(ah.AdditionalContext, bh.AdditionalContext)
	if ah.UpdatedInput == nil {
		ah.UpdatedInput = bh.UpdatedInput
	}
	return a
}

func severity(decision string) int {
	switch decision {
	case payload.DecisionDeny:
		return 3
	case payload.DecisionAsk:
		return 2
	case payload.DecisionAllow:
		return 1
	default:
		return 0
	}
}

func joinNonEmpty(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	default:
		return a + "\n\n" + b
	}
}
