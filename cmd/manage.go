package cmd

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/zealot00/claude-toolkit/internal/capcfg"
)

// manageFlags carries the options manage accepts. Capability state lives in
// the toolkit's private capcfg (not Claude Code's settings.json), so manage
// is global and takes no scope.
type manageFlags struct {
	dryRun bool
}

func parseManageFlags(args []string) (manageFlags, []string) {
	f := manageFlags{}
	var rest []string
	for _, a := range args {
		switch a {
		case "--dry-run":
			f.dryRun = true
		case "--help", "-h":
			rest = append(rest, "help")
		default:
			rest = append(rest, a)
		}
	}
	return f, rest
}

func manageUsage() {
	fmt.Fprintf(os.Stderr, `Usage: %s manage [--dry-run] [list | enable <cap>... | disable <cap>... | enable-all | disable-all]

Without a subcommand, opens an interactive toggle UI. The subcommands are
what Claude Code uses through the /toolkit plugin command.

Capability state is global and stored in the toolkit's private config
(%s), separate from Claude Code's settings.json, so it survives plugin
disable/uninstall and works for both the settings.json and plugin hook
registration paths.

Flags:
  --dry-run   show what would change without writing
`, binName, capcfgPathLabel())
}

// manageCmd lets the user — or Claude itself through the plugin's /toolkit
// command — see and toggle which hook capabilities are enabled.
func manageCmd(args []string) int {
	f, rest := parseManageFlags(args)

	if len(rest) == 0 {
		return manageTUI(f.dryRun)
	}
	switch rest[0] {
	case "list", "status":
		return manageList()
	case "enable", "disable":
		names := rest[1:]
		if len(names) == 0 {
			fmt.Fprintf(os.Stderr, "error: %s needs at least one capability name\n\n", rest[0])
			manageUsage()
			return 2
		}
		return manageSet(names, rest[0] == "enable", f.dryRun)
	case "enable-all":
		return manageSetAll(true, f.dryRun)
	case "disable-all":
		return manageSetAll(false, f.dryRun)
	case "help", "--help", "-h":
		manageUsage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "error: unknown manage subcommand %q\n\n", rest[0])
		manageUsage()
		return 2
	}
}

// manageList prints every capability with its enabled/disabled state.
func manageList() int {
	enabled, err := enabledCapabilities()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	caps := registeredCapabilities()

	fmt.Printf("claude-toolkit hooks (global state: %s)\n\n", capcfgPathLabel())
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "capability\tevent(s)\tmatcher\tstate")
	enabledCount := 0
	for _, c := range caps {
		state := "disabled"
		if enabled[c.name] {
			state = "enabled"
			enabledCount++
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", c.name, strings.Join(c.events, ", "), c.matcher, state)
	}
	w.Flush()
	fmt.Printf("\n%d capability(ies), %d enabled, %d disabled.\n", len(caps), enabledCount, len(caps)-enabledCount)
	return 0
}

// manageSet enables or disables the named capabilities.
func manageSet(names []string, want, dryRun bool) int {
	valid := map[string]bool{}
	for _, c := range registeredCapabilities() {
		valid[c.name] = true
	}
	for _, n := range names {
		if !valid[n] {
			fmt.Fprintf(os.Stderr, "error: unknown capability %q; valid names: %s\n", n, strings.Join(enabledKeys(valid), ", "))
			return 2
		}
	}

	enabled, err := enabledCapabilities()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	for _, n := range names {
		enabled[n] = want
	}
	return applyEnableState(enabled, names, dryRun)
}

// manageSetAll enables or disables every capability.
func manageSetAll(want, dryRun bool) int {
	enabled := map[string]bool{}
	all := []string{}
	for _, c := range registeredCapabilities() {
		enabled[c.name] = want
		all = append(all, c.name)
	}
	return applyEnableState(enabled, all, dryRun)
}

// applyEnableState persists the enabled set and reports the change. changed
// is the list of capability names the caller asked to modify; the message
// loop reports only those, not every enabled capability.
func applyEnableState(enabled map[string]bool, changed []string, dryRun bool) int {
	verb := func(want bool) string {
		if want {
			return "enabled"
		}
		return "disabled"
	}
	if !dryRun {
		if err := capcfg.Save(enabled); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
	}
	for _, n := range changed {
		fmt.Printf("  %s %s\n", verb(enabled[n]), n)
	}
	if dryRun {
		fmt.Printf("\n(dry run) would write %s\n", capcfgPathLabel())
		return 0
	}
	fmt.Printf("updated %s\n", capcfgPathLabel())
	fmt.Println("Hooks load at session start; restart Claude Code (or start a new session) for the change to take effect.")
	return 0
}

// manageTUI is the interactive toggle for a human in a terminal.
func manageTUI(dryRun bool) int {
	enabled, err := enabledCapabilities()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	caps := registeredCapabilities()

	sc := bufio.NewScanner(os.Stdin)
	for {
		fmt.Printf("\nclaude-toolkit hooks (global state)\n\n")
		for i, c := range caps {
			state := "disabled"
			if enabled[c.name] {
				state = "enabled"
			}
			fmt.Printf("  [%d] %-8s %-16s %s\n", i+1, c.name, strings.Join(c.events, ", "), state)
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

		if dryRun {
			fmt.Printf("  (dry run) would write %s\n", capcfgPathLabel())
			continue
		}
		if err := capcfg.Save(enabled); err != nil {
			fmt.Printf("  error: %v\n", err)
			continue
		}
		fmt.Printf("  updated %s\n", capcfgPathLabel())
	}
}

// capcfgPathLabel renders the capability state file path for messages.
func capcfgPathLabel() string {
	if p, err := capcfg.Path(); err == nil {
		return p
	}
	return "~/.claude-toolkit/state/capabilities.json"
}

// enabledKeys returns the names whose value is true, sorted.
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
