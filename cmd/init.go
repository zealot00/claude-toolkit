package cmd

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/zealot00/claude-toolkit/internal/payload"
	"github.com/zealot00/claude-toolkit/pkg/installer"
)

// hookTimeouts bounds each event. PreToolUse sits in front of every tool call
// and must stay imperceptible; PostToolUse may shell out to a formatter on a
// cold cache and needs room.
var hookTimeouts = map[string]int{
	payload.EventPreToolUse:   10,
	payload.EventPostToolUse:  30,
	payload.EventSessionStart: 15,
}

// buildSpecs derives the settings.json entries from the routes the binary
// actually registers, so the two can never disagree. caps restricts which
// capabilities get an entry; empty installs every capability. Each capability
// becomes its own matcher group whose command names it via --cap, which is
// what lets `manage disable <cap>` remove one capability without touching its
// siblings on the same event.
func buildSpecs(command string, caps ...string) []installer.Spec {
	requested := map[string]bool{}
	for _, c := range caps {
		requested[c] = true
	}
	specs := make([]installer.Spec, 0, len(registeredCapabilities()))
	for _, cap := range registeredCapabilities() {
		if len(caps) > 0 && !requested[cap.name] {
			continue
		}
		alias, ok := eventAlias[cap.event]
		if !ok {
			alias = cap.event
		}
		specs = append(specs, installer.Spec{
			Event:      cap.event,
			Capability: cap.name,
			Matcher:    cap.matcher,
			Command:    fmt.Sprintf("%s run --event=%s --cap=%s", command, alias, cap.name),
			Timeout:    hookTimeouts[cap.event],
		})
	}
	return specs
}

// shellQuote wraps a path in double quotes when it contains whitespace, so the
// generated command survives the shell Claude Code runs it through.
func shellQuote(p string) string {
	if strings.ContainsAny(p, " \t") {
		return `"` + p + `"`
	}
	return p
}

func initCmd(args []string) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "show what would change without writing anything")
	scope := fs.String("scope", string(installer.ScopeUser), "which settings file to write: user, project or local")
	projectDir := fs.String("project-dir", "", "project root for --scope=project|local (default: current directory)")
	uninstall := fs.Bool("uninstall", false, "remove the toolkit's hooks instead of installing them")
	absPath := fs.Bool("abs-path", false, "pin the absolute path of this binary instead of resolving claude-toolkit on PATH")
	force := fs.Bool("force", false, "install even if the binary cannot be resolved on PATH")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s init [flags]\n\n"+
			"Merges the toolkit's hooks into a Claude Code settings file. Existing hooks\n"+
			"and every other setting are preserved; the file is backed up before writing.\n\nFlags:\n", binName)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	dir := *projectDir
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: determine working directory: %v\n", err)
			return 1
		}
		dir = cwd
	}

	path, err := installer.Path(installer.Scope(*scope), dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	var specs []installer.Spec
	if !*uninstall {
		command, err := resolveCommand(*absPath, *force)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return 1
		}
		specs = buildSpecs(command)
	}

	plan, err := installer.BuildPlan(path, specs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	action := "Installing"
	if *uninstall {
		action = "Removing"
	}
	fmt.Printf("%s claude-toolkit hooks in %s\n\n", action, path)

	if !plan.Changed() {
		fmt.Println("Already up to date; nothing to do.")
		return 0
	}
	fmt.Println(plan.Summary())

	if *dryRun {
		fmt.Printf("\nResulting hooks section:\n\n%s\n\n(dry run -- nothing written)\n", plan.HooksJSON())
		return 0
	}

	if err := plan.Apply(); err != nil {
		fmt.Fprintf(os.Stderr, "\nerror: %v\n", err)
		return 1
	}

	fmt.Println()
	if plan.BackupPath != "" {
		fmt.Printf("Backed up previous settings to %s\n", plan.BackupPath)
	}
	fmt.Printf("Wrote %s\n", plan.Path)
	fmt.Printf("\nHooks are loaded when a session starts, so restart Claude Code for this to\n" +
		"take effect. Then run `claude-toolkit doctor` to verify.\n")
	return 0
}

// resolveCommand decides what command string goes into settings.json.
//
// The default is a bare PATH lookup, which is what makes the config portable
// across machines. That only works if Claude Code's environment can actually
// find the binary -- and on macOS a GUI-launched app does not inherit the PATH
// from a shell profile, so `~/go/bin` is frequently missing. Discovering that
// at install time is cheap; discovering it later means hooks that silently
// never fire.
func resolveCommand(absPath, force bool) (string, error) {
	self, err := os.Executable()
	if err == nil {
		if resolved, rerr := filepath.EvalSymlinks(self); rerr == nil {
			self = resolved
		}
	}

	if absPath {
		if self == "" {
			return "", fmt.Errorf("error: --abs-path requested but this binary's own path could not be determined")
		}
		return shellQuote(self), nil
	}

	found, lookErr := exec.LookPath(binName)
	if lookErr != nil {
		if !force {
			return "", fmt.Errorf(`error: %q is not on PATH, so Claude Code would not be able to run the hooks.

Pick one:
  - Add the install directory to PATH (for go install, that is %s), then re-run.
  - Or run "%s init --abs-path" to hard-code this binary's location instead.
  - Or run "%s init --force" to write the config anyway.`,
				binName, goBinDir(), binName, binName)
		}
		fmt.Fprintf(os.Stderr, "warning: %q is not on PATH; the hooks will not fire until it is\n\n", binName)
		return binName, nil
	}

	if self != "" {
		if resolved, rerr := filepath.EvalSymlinks(found); rerr == nil {
			found = resolved
		}
		if found != self {
			fmt.Fprintf(os.Stderr,
				"warning: PATH resolves %s to\n  %s\nbut this process is\n  %s\n"+
					"The hooks will run the copy on PATH. Use --abs-path to pin this one.\n\n",
				binName, found, self)
		}
	}
	return binName, nil
}

// goBinDir reports where `go install` places binaries, for the error message.
func goBinDir() string {
	if p := os.Getenv("GOBIN"); p != "" {
		return p
	}
	if p := os.Getenv("GOPATH"); p != "" {
		return filepath.Join(p, "bin")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, "go", "bin")
	}
	return "$GOPATH/bin"
}
