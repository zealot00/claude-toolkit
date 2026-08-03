package cmd

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/zealot00/claude-toolkit/pkg/installer"
)

// manageFlags holds the options shared by every manage subcommand. They are
// parsed by hand rather than with flag.FlagSet because the subcommand and
// capability names are positional: the standard library stops parsing at the
// first non-flag argument, which would make `manage enable guard --dry-run`
// read --dry-run as a capability name.
type manageFlags struct {
	scope      string
	projectDir string
	dryRun     bool
	absPath    bool
	force      bool
}

func parseManageFlags(args []string) (manageFlags, []string) {
	f := manageFlags{scope: string(installer.ScopeUser)}
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--dry-run":
			f.dryRun = true
		case a == "--abs-path":
			f.absPath = true
		case a == "--force":
			f.force = true
		case a == "--scope" || strings.HasPrefix(a, "--scope="):
			if v, n := flagValue(a, "--scope", args, i); n >= 0 {
				f.scope, i = v, n
			}
		case a == "--project-dir" || strings.HasPrefix(a, "--project-dir="):
			if v, n := flagValue(a, "--project-dir", args, i); n >= 0 {
				f.projectDir, i = v, n
			}
		case a == "--help" || a == "-h":
			rest = append(rest, "help")
		default:
			rest = append(rest, a)
		}
	}
	return f, rest
}

// flagValue resolves --name=value (returning i unchanged) or --name value
// (returning the next index), or reports failure with n < 0.
func flagValue(arg, name string, args []string, i int) (string, int) {
	if v, ok := strings.CutPrefix(arg, name+"="); ok {
		return v, i
	}
	if i+1 < len(args) {
		return args[i+1], i + 1
	}
	return "", -1
}

func manageUsage() {
	fmt.Fprintf(os.Stderr, `Usage: %s manage [flags] [list | enable <cap>... | disable <cap>... | enable-all | disable-all]

Without a subcommand, opens an interactive toggle UI. The subcommands are
what Claude Code uses through the /toolkit plugin command.

Flags (may appear before or after the subcommand):
  --scope user|project|local   which settings file to manage (default user)
  --project-dir <path>         project root for --scope=project|local
  --dry-run                    show what would change without writing
  --abs-path                   pin this binary's absolute path in the config
  --force                      proceed even if the binary is not on PATH
`, binName)
}

// manageCmd lets the user — or Claude itself through the plugin's /toolkit
// command — see and toggle which hook capabilities are installed in a Claude
// Code settings file.
//
//	claude-toolkit manage             interactive toggle UI (terminal)
//	claude-toolkit manage list        print capabilities and their state
//	claude-toolkit manage enable <cap>...
//	claude-toolkit manage disable <cap>...
//	claude-toolkit manage enable-all
//	claude-toolkit manage disable-all
//
// The subcommand forms are the interface Claude Code uses via the plugin's
// /toolkit command, so their output is deliberately plain and parseable.
func manageCmd(args []string) int {
	f, rest := parseManageFlags(args)

	dir := f.projectDir
	if dir == "" {
		dir, _ = os.Getwd()
	}
	path, err := installer.Path(installer.Scope(f.scope), dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	if len(rest) == 0 {
		return manageTUI(path, f)
	}
	switch rest[0] {
	case "list", "status":
		return manageList(path)
	case "enable", "disable":
		names := rest[1:]
		if len(names) == 0 {
			fmt.Fprintf(os.Stderr, "error: %s needs at least one capability name\n\n", rest[0])
			manageUsage()
			return 2
		}
		return manageSet(path, names, rest[0] == "enable", f.dryRun, f.absPath, f.force)
	case "enable-all":
		return manageSet(path, nil, true, f.dryRun, f.absPath, f.force)
	case "disable-all":
		return manageSet(path, nil, false, f.dryRun, f.absPath, f.force)
	case "help", "--help", "-h":
		manageUsage()
		return 0
	default:
		if strings.HasPrefix(rest[0], "-") {
			fmt.Fprintf(os.Stderr, "error: unknown flag %q\n\n", rest[0])
		} else {
			fmt.Fprintf(os.Stderr, "error: unknown manage subcommand %q\n\n", rest[0])
		}
		manageUsage()
		return 2
	}
}

// manageList prints every capability with its enabled/disabled state. The
// output is tab-aligned so both a human and Claude can read it.
func manageList(path string) int {
	enabled, err := enabledCapabilities(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	caps := registeredCapabilities()
	legacy, err := legacyCapabilities(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	if !fileExists(path) {
		fmt.Printf("claude-toolkit is not installed in %s\n", path)
		fmt.Printf("Run `%s init` to register its hooks, then manage them here.\n", binName)
		return 0
	}

	fmt.Printf("claude-toolkit hooks (%s)\n\n", path)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "capability\tevent\tmatcher\tstate")
	enabledCount := 0
	for _, c := range caps {
		state := "disabled"
		if enabled[c.name] {
			state = "enabled"
			enabledCount++
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", c.name, c.event, c.matcher, state)
	}
	w.Flush()
	fmt.Printf("\n%d capability(ies), %d enabled, %d disabled.\n", len(caps), enabledCount, len(caps)-enabledCount)
	if legacy > 0 {
		fmt.Printf("! %d legacy hook entr%s without capability tags. Run `%s init` once to migrate, then manage here.\n",
			legacy, plural(legacy), binName)
	}
	return 0
}

// manageSet enables or disables the named capabilities (or all of them when
// names is nil, as enable-all/disable-all do) and rewrites the settings file.
func manageSet(path string, names []string, want, dryRun, absPath, force bool) int {
	valid := map[string]bool{}
	for _, c := range registeredCapabilities() {
		valid[c.name] = true
	}

	selected := names
	if names == nil { // enable-all / disable-all
		selected = make([]string, 0, len(valid))
		for n := range valid {
			selected = append(selected, n)
		}
		sort.Strings(selected)
	} else {
		for _, n := range names {
			if !valid[n] {
				fmt.Fprintf(os.Stderr, "error: unknown capability %q; valid names: %s\n", n, strings.Join(enabledKeys(valid), ", "))
				return 2
			}
		}
	}

	enabled, err := enabledCapabilities(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if legacy, err := legacyCapabilities(path); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	} else if legacy > 0 {
		fmt.Fprintf(os.Stderr, "error: %d legacy hook entr%s without capability tags in %s.\n"+
			"Run `%s init` once to migrate them, then retry.\n",
			legacy, plural(legacy), path, binName)
		return 1
	}
	for _, n := range selected {
		enabled[n] = want
	}

	command, err := resolveCommand(absPath, force)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}

	plan, err := capabilityPlan(path, command, enabled)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if !plan.Changed() {
		fmt.Println("no change; the requested capabilities are already in that state")
		return 0
	}

	verb := "disabled"
	if want {
		verb = "enabled"
	}
	for _, n := range selected {
		fmt.Printf("  %s %s\n", verb, n)
	}

	if dryRun {
		fmt.Printf("\n(dry run) would write %s:\n%s\n\nResulting hooks section:\n\n%s\n",
			path, plan.Summary(), plan.HooksJSON())
		return 0
	}
	if err := plan.Apply(); err != nil {
		fmt.Fprintf(os.Stderr, "\nerror: %v\n", err)
		return 1
	}
	fmt.Printf("updated %s\n", path)
	fmt.Println("Restart Claude Code (or start a new session) for the change to take effect.")
	return 0
}

// manageTUI is the interactive toggle for a human in a terminal. Claude Code
// itself uses the subcommand forms instead; its stdin is not a terminal.
func manageTUI(path string, f manageFlags) int {
	if !fileExists(path) {
		fmt.Printf("claude-toolkit is not installed in %s\n", path)
		fmt.Printf("Run `%s init` to register its hooks, then run `%s manage` again.\n", binName, binName)
		return 1
	}
	if legacy, err := legacyCapabilities(path); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	} else if legacy > 0 {
		fmt.Fprintf(os.Stderr, "%d legacy hook entr%s without capability tags in %s.\n"+
			"Run `%s init` once to migrate them, then retry.\n",
			legacy, plural(legacy), path, binName)
		return 1
	}
	command, err := resolveCommand(f.absPath, f.force)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}

	caps := registeredCapabilities()
	enabled, err := enabledCapabilities(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	sc := bufio.NewScanner(os.Stdin)
	for {
		fmt.Printf("\nclaude-toolkit hooks (%s)\n\n", path)
		for i, c := range caps {
			state := "disabled"
			if enabled[c.name] {
				state = "enabled"
			}
			fmt.Printf("  [%d] %-8s %-12s %s\n", i+1, c.name, c.event, state)
		}
		fmt.Printf("\n  1-%d: toggle   a: enable all   n: disable all   q: quit\n> ", len(caps))

		if !sc.Scan() {
			return 0
		}
		line := strings.TrimSpace(sc.Text())
		switch line {
		case "q", "quit", "":
			return 0
		case "a":
			for _, c := range caps {
				enabled[c.name] = true
			}
		case "n":
			for _, c := range caps {
				enabled[c.name] = false
			}
		default:
			idx, err := strconv.Atoi(line)
			if err != nil || idx < 1 || idx > len(caps) {
				fmt.Println("  ?")
				continue
			}
			c := caps[idx-1]
			enabled[c.name] = !enabled[c.name]
		}

		plan, err := capabilityPlan(path, command, enabled)
		if err != nil {
			fmt.Printf("  error: %v\n", err)
			continue
		}
		if plan.Changed() {
			if err := plan.Apply(); err != nil {
				fmt.Printf("  error: %v\n", err)
				continue
			}
			fmt.Printf("  updated %s\n", path)
		}
	}
}

// capabilityPlan computes the settings write that results from the given set
// of enabled capabilities. It reads but never writes; manageSet and the TUI
// apply the plan, and tests use it to verify enable/disable round-trips.
//
// An empty set means "disable everything", not "install everything": buildSpecs
// treats an empty caps list as "no filter", so we must bypass it and pass nil
// specs (the installer's uninstall contract) instead.
func capabilityPlan(path, command string, enabled map[string]bool) (*installer.Plan, error) {
	names := enabledKeys(enabled)
	if len(names) == 0 {
		return installer.BuildPlan(path, nil)
	}
	return installer.BuildPlan(path, buildSpecs(command, names...))
}

// enabledKeys returns the names whose value is true, sorted. buildSpecs
// treats "listed = enabled", so disabled capabilities must be dropped from
// the map rather than kept with a false value.
func enabledKeys(m map[string]bool) []string {
	var out []string
	for k, v := range m {
		if v {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// plural completes "entr" + suffix -> "entry"/"entries" in messages.
func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
