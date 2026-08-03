// Package installer merges claude-toolkit's hook registrations into a Claude
// Code settings file.
//
// The settings file is user property. It holds API tokens, model overrides and
// hooks the user configured by hand, and this package is the only thing in the
// toolkit that writes to it. Three rules follow from that:
//
//  1. Never replace the whole "hooks" object. Merge per event, and leave hook
//     entries the toolkit did not create exactly where they are.
//  2. Never write a file that failed to parse. A settings file we cannot read
//     is a settings file we must not clobber.
//  3. Never write in place. Back up, write a temp file, rename. A process
//     killed mid-write must not be able to truncate the user's credentials.
package installer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Scope selects which settings file to operate on.
type Scope string

const (
	// ScopeUser is ~/.claude/settings.json, applying to every project.
	ScopeUser Scope = "user"
	// ScopeProject is <project>/.claude/settings.json, checked into the repo.
	ScopeProject Scope = "project"
	// ScopeLocal is <project>/.claude/settings.local.json, gitignored.
	ScopeLocal Scope = "local"
)

// Path resolves the settings file for a scope. projectDir is ignored for
// ScopeUser.
func Path(scope Scope, projectDir string) (string, error) {
	switch scope {
	case ScopeUser:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("installer: locate home directory: %w", err)
		}
		return filepath.Join(home, ".claude", "settings.json"), nil
	case ScopeProject:
		return filepath.Join(projectDir, ".claude", "settings.json"), nil
	case ScopeLocal:
		return filepath.Join(projectDir, ".claude", "settings.local.json"), nil
	default:
		return "", fmt.Errorf("installer: unknown scope %q (want user, project or local)", scope)
	}
}

// Spec is one hook registration to inject.
type Spec struct {
	Event   string // e.g. "PreToolUse"
	Matcher string // e.g. "Bash|Write|Edit"
	Command string // e.g. "claude-toolkit run --event=pre"
	Timeout int    // seconds; omitted from JSON when zero
}

// ownedCommand recognises a hook entry this toolkit installed, whether it was
// written as a bare PATH lookup or as an absolute path, and whatever the
// binary's directory was at the time. Matching on the invocation rather than
// on an exact string means reinstalling after moving the binary replaces the
// old entry instead of leaving a dead one behind.
var ownedCommand = regexp.MustCompile(`(^|[/\\])claude-toolkit(\.exe)?["']?\s+run(\s|$)`)

// IsOwned reports whether a hook command belongs to this toolkit.
func IsOwned(command string) bool { return ownedCommand.MatchString(command) }

// Plan is a computed but unapplied change to a settings file. Building a plan
// never touches disk beyond reading, so `init --dry-run` and `doctor` can use
// the same code path as a real install.
type Plan struct {
	// Path is the settings file the plan targets.
	Path string
	// Before is the file's current bytes; nil when it does not exist.
	Before []byte
	// After is the bytes that Apply will write.
	After []byte
	// Mode is the file mode Apply will use.
	Mode os.FileMode
	// BackupPath is where Before will be copied, empty when there is nothing
	// to back up.
	BackupPath string

	// Added, Replaced and Removed summarise the change, one entry per event.
	Added    []string
	Replaced []string
	Removed  []string
}

// Changed reports whether applying the plan would alter the file.
func (p *Plan) Changed() bool { return !bytes.Equal(p.Before, p.After) }

// Summary renders the plan's effect as human-readable lines.
func (p *Plan) Summary() string {
	if !p.Changed() {
		return "settings already up to date; nothing to do"
	}
	var b strings.Builder
	for _, e := range p.Added {
		fmt.Fprintf(&b, "  + %s\n", e)
	}
	for _, e := range p.Replaced {
		fmt.Fprintf(&b, "  ~ %s (replaced existing claude-toolkit entry)\n", e)
	}
	for _, e := range p.Removed {
		fmt.Fprintf(&b, "  - %s\n", e)
	}
	return strings.TrimRight(b.String(), "\n")
}

// HooksJSON returns just the resulting "hooks" object, for display.
func (p *Plan) HooksJSON() string {
	var root map[string]any
	if err := json.Unmarshal(p.After, &root); err != nil {
		return ""
	}
	h, ok := root["hooks"]
	if !ok {
		return "{}"
	}
	out, err := json.MarshalIndent(map[string]any{"hooks": h}, "", "  ")
	if err != nil {
		return ""
	}
	return string(out)
}

// BuildPlan computes the settings file that results from installing specs.
// Passing no specs computes a removal of every entry the toolkit owns.
func BuildPlan(path string, specs []Spec) (*Plan, error) {
	before, mode, err := read(path)
	if err != nil {
		return nil, err
	}

	root, err := decode(before, path)
	if err != nil {
		return nil, err
	}

	hooks, err := childObject(root, "hooks", path)
	if err != nil {
		return nil, err
	}

	p := &Plan{Path: path, Before: before, Mode: mode}

	// Every event we own must be swept, both the ones we are installing and
	// any left over from a previous version that no longer registers them.
	events := map[string]bool{}
	for _, s := range specs {
		events[s.Event] = true
	}
	for ev := range hooks {
		events[ev] = true
	}

	for _, ev := range sortedKeys(events) {
		groups, err := eventGroups(hooks, ev, path)
		if err != nil {
			return nil, err
		}
		kept, evicted := stripOwned(groups)

		var spec *Spec
		for i := range specs {
			if specs[i].Event == ev {
				spec = &specs[i]
				break
			}
		}

		if spec != nil {
			kept = append(kept, group(*spec))
			if evicted > 0 {
				p.Replaced = append(p.Replaced, ev)
			} else {
				p.Added = append(p.Added, ev)
			}
		} else if evicted > 0 {
			p.Removed = append(p.Removed, ev)
		}

		if len(kept) == 0 {
			delete(hooks, ev)
			continue
		}
		hooks[ev] = kept
	}

	if len(hooks) == 0 {
		delete(root, "hooks")
	} else {
		root["hooks"] = hooks
	}

	after, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("installer: encode settings: %w", err)
	}
	p.After = append(after, '\n')

	if len(before) > 0 && p.Changed() {
		p.BackupPath = fmt.Sprintf("%s.bak.%s", path, time.Now().Format("20060102-150405"))
	}
	return p, nil
}

// Apply writes the plan: backup, then atomic rename into place.
func (p *Plan) Apply() error {
	if !p.Changed() {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p.Path), 0o755); err != nil {
		return fmt.Errorf("installer: create settings directory: %w", err)
	}
	if p.BackupPath != "" {
		if err := os.WriteFile(p.BackupPath, p.Before, p.Mode); err != nil {
			return fmt.Errorf("installer: write backup %s: %w", p.BackupPath, err)
		}
	}
	return atomicWrite(p.Path, p.After, p.Mode)
}

// read returns the file's contents and mode. A missing file yields nil bytes
// and 0600, because a settings file we create will hold an API token and has
// no business being world-readable.
func read(path string) ([]byte, os.FileMode, error) {
	st, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil, 0o600, nil
	}
	if err != nil {
		return nil, 0, fmt.Errorf("installer: stat %s: %w", path, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, fmt.Errorf("installer: read %s: %w", path, err)
	}
	return data, st.Mode().Perm(), nil
}

// decode parses the settings file into an ordered-safe generic map. UseNumber
// keeps numeric literals exactly as written, so a timeout of 3000000 does not
// come back out as 3e+06.
func decode(data []byte, path string) (map[string]any, error) {
	root := map[string]any{}
	if len(bytes.TrimSpace(data)) == 0 {
		return root, nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&root); err != nil {
		return nil, fmt.Errorf(
			"installer: %s is not valid JSON (%w).\n"+
				"Refusing to overwrite it -- fix or move the file, then re-run", path, err)
	}
	return root, nil
}

func childObject(root map[string]any, key, path string) (map[string]any, error) {
	v, ok := root[key]
	if !ok || v == nil {
		return map[string]any{}, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("installer: %s: %q is %T, expected an object; refusing to overwrite", path, key, v)
	}
	return m, nil
}

func eventGroups(hooks map[string]any, event, path string) ([]any, error) {
	v, ok := hooks[event]
	if !ok || v == nil {
		return nil, nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("installer: %s: hooks.%s is %T, expected an array; refusing to overwrite", path, event, v)
	}
	return arr, nil
}

// stripOwned removes matcher groups belonging to this toolkit, returning the
// groups to keep and how many entries were evicted. A group that contained
// both our entry and a user's keeps the user's.
func stripOwned(groups []any) (kept []any, evicted int) {
	for _, g := range groups {
		gm, ok := g.(map[string]any)
		if !ok {
			kept = append(kept, g)
			continue
		}
		entries, ok := gm["hooks"].([]any)
		if !ok {
			kept = append(kept, g)
			continue
		}
		var survivors []any
		for _, e := range entries {
			em, ok := e.(map[string]any)
			if !ok {
				survivors = append(survivors, e)
				continue
			}
			if cmd, ok := em["command"].(string); ok && IsOwned(cmd) {
				evicted++
				continue
			}
			survivors = append(survivors, e)
		}
		if len(survivors) == 0 {
			continue // the whole group was ours
		}
		gm["hooks"] = survivors
		kept = append(kept, gm)
	}
	return kept, evicted
}

// group renders a Spec as a settings.json matcher group.
func group(s Spec) map[string]any {
	entry := map[string]any{"type": "command", "command": s.Command}
	if s.Timeout > 0 {
		entry["timeout"] = s.Timeout
	}
	g := map[string]any{"hooks": []any{entry}}
	if s.Matcher != "" {
		g["matcher"] = s.Matcher
	}
	return g
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".claude-toolkit-*.tmp")
	if err != nil {
		return fmt.Errorf("installer: create temp file in %s: %w", dir, err)
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op once the rename succeeds

	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("installer: write temp file: %w", err)
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return fmt.Errorf("installer: chmod temp file: %w", err)
	}
	// Flush to disk before the rename, so a crash cannot leave the settings
	// path pointing at an empty inode.
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("installer: sync temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("installer: close temp file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("installer: rename into %s: %w", path, err)
	}
	return nil
}

// Installed describes one hook entry belonging to this toolkit that is
// currently present in a settings file.
type Installed struct {
	Event   string
	Matcher string
	Command string
	Timeout int
}

// Inspect reports the toolkit's own hook entries in a settings file. A missing
// file yields no entries and no error, since "not installed" is a legitimate
// state for doctor to describe rather than an failure to read.
func Inspect(path string) ([]Installed, error) {
	data, _, err := read(path)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	root, err := decode(data, path)
	if err != nil {
		return nil, err
	}
	hooks, err := childObject(root, "hooks", path)
	if err != nil {
		return nil, err
	}

	var out []Installed
	for _, ev := range sortedStrings(hooks) {
		groups, err := eventGroups(hooks, ev, path)
		if err != nil {
			return nil, err
		}
		for _, g := range groups {
			gm, ok := g.(map[string]any)
			if !ok {
				continue
			}
			matcher, _ := gm["matcher"].(string)
			entries, ok := gm["hooks"].([]any)
			if !ok {
				continue
			}
			for _, e := range entries {
				em, ok := e.(map[string]any)
				if !ok {
					continue
				}
				cmd, ok := em["command"].(string)
				if !ok || !IsOwned(cmd) {
					continue
				}
				in := Installed{Event: ev, Matcher: matcher, Command: cmd}
				if n, ok := em["timeout"].(json.Number); ok {
					if v, err := n.Int64(); err == nil {
						in.Timeout = int(v)
					}
				}
				out = append(out, in)
			}
		}
	}
	return out, nil
}

func sortedStrings(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
