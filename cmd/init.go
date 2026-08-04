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
// cold cache and needs room. Stop and SessionEnd fire infrequently but
// retryguard reads the transcript tail, so they need a few seconds of slack.
var hookTimeouts = map[string]int{
	payload.EventPreToolUse:         10,
	payload.EventPostToolUse:        30,
	payload.EventPostToolUseFailure: 30,
	payload.EventSessionStart:       15,
	payload.EventSessionEnd:         5,
	payload.EventUserPromptSubmit:   5,
	payload.EventStop:               10,
}

// buildSpecs derives the settings.json entries from the routes the binary
// actually registers, so the two can never disagree. caps restricts which
// capabilities get an entry; empty installs every capability. Each
// capability gets one matcher group PER EVENT it listens on, each carrying
// --cap=<name> and --event=<alias>, so disabling one capability removes all
// of its groups without touching sibling capabilities on the same event.
func buildSpecs(command string, caps ...string) []installer.Spec {
	requested := map[string]bool{}
	for _, c := range caps {
		requested[c] = true
	}
	var specs []installer.Spec
	for _, cap := range registeredCapabilities() {
		if len(caps) > 0 && !requested[cap.name] {
			continue
		}
		for _, ev := range cap.events {
			alias, ok := eventAlias[ev]
			if !ok {
				alias = ev
			}
			specs = append(specs, installer.Spec{
				Event:      ev,
				Capability: cap.name,
				Matcher:    cap.matcher,
				Command:    fmt.Sprintf("%s run --event=%s --cap=%s", command, alias, cap.name),
				Timeout:    hookTimeouts[ev],
			})
		}
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
	scope := fs.String("scope", "skills-user", "target: skills-user|skills-project (plugin directory, idiomatic) or user|project|local (legacy settings.json paths)")
	projectDir := fs.String("project-dir", "", "project root for --scope=project|local|skills-project (default: current directory)")
	uninstall := fs.Bool("uninstall", false, "remove the toolkit's hooks instead of installing them")
	absPath := fs.Bool("abs-path", false, "pin the absolute path of this binary (now the default; kept for compatibility)")
	noAbsPath := fs.Bool("no-abs-path", false, "resolve the command on PATH instead of pinning this binary's absolute path")
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
	_ = *absPath // kept for compatibility: pinning the absolute path is the default now

	dir := *projectDir
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: determine working directory: %v\n", err)
			return 1
		}
		dir = cwd
	}

	// skills-* scopes install the auto-loading plugin directory instead of
	// touching settings.json. After the plugin install, also inject
	// statusLine into the user's settings.json -- Claude Code reads it
	// from the user-level settings, not from the plugin manifest, and the
	// plugin install path does not get to set it.
	if *scope == "skills-user" || *scope == "skills-project" {
		rc := initSkillsScope(*scope, dir, *dryRun)
		if rc != 0 {
			return rc
		}
		if !*uninstall {
			if err := injectUserStatusLine(*dryRun); err != nil {
				fmt.Fprintf(os.Stderr, "warning: statusLine not injected: %v\n", err)
			}
		}
		return rc
	}

	// Legacy settings.json paths (--scope=user|project|local). The plugin
	// directory is the idiomatic Claude Code install surface; warn users
	// who still have toolkit hooks in settings.json that they can switch
	// with one command and get plugin-aware lifecycle for free.
	if !*uninstall {
		if homePath, err := installer.Path(installer.ScopeUser, ""); err == nil {
			if installed, _ := installer.Inspect(homePath); len(installed) > 0 {
				fmt.Fprintf(os.Stderr,
					"note: claude-toolkit hooks are registered in %s, which means\n"+
						"      /plugin disable and /plugin uninstall cannot turn them off. Run\n"+
						"        %s init --scope=skills-user\n"+
						"      to move the install to the plugin directory and reclaim that lifecycle.\n\n",
					homePath, binName)
			}
		}
	}

	path, err := installer.Path(installer.Scope(*scope), dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	var specs []installer.Spec
	if !*uninstall {
		command, err := resolveCommand(*noAbsPath, *force)
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

	// statusLine is a top-level field, not a hook event. Inject it
	// separately so HUD can render without competing with the hook merge.
	// Only meaningful when we are installing, not uninstalling.
	statusPlan := (*installer.Plan)(nil)
	if !*uninstall {
		command, _ := resolveCommand(*noAbsPath, *force)
		statusPlan, err = installer.EnsureStatusLine(path, installer.StatusLineConfig{
			Type:    "command",
			Command: command + " hud",
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
	}

	action := "Installing"
	if *uninstall {
		action = "Removing"
	}
	fmt.Printf("%s claude-toolkit hooks in %s\n\n", action, path)

	if !plan.Changed() && (statusPlan == nil || !statusPlan.Changed()) {
		fmt.Println("Already up to date; nothing to do.")
		return 0
	}
	if plan.Changed() {
		fmt.Println(plan.Summary())
	}
	if statusPlan != nil && statusPlan.Changed() {
		fmt.Println("  + statusLine (HUD)")
	}
	if statusPlan != nil && statusPlan.StatusLineKept != "" {
		fmt.Println("  · statusLine: kept existing (" + statusPlan.StatusLineKept + "); HUD will not render until you switch")
	}

	if *dryRun {
		fmt.Printf("\nResulting hooks section:\n\n%s\n\n(dry run -- nothing written)\n", plan.HooksJSON())
		if _, err := installPlugin(true, false); err == nil {
			fmt.Println("(dry run -- would also install the Claude Code plugin to ~/.claude/plugins/claude-toolkit/)")
		}
		return 0
	}

	if err := plan.Apply(); err != nil {
		fmt.Fprintf(os.Stderr, "\nerror: %v\n", err)
		return 1
	}
	if statusPlan != nil {
		if err := statusPlan.Apply(); err != nil {
			fmt.Fprintf(os.Stderr, "\nerror: %v\n", err)
			return 1
		}
	}

	fmt.Println()
	if plan.BackupPath != "" {
		fmt.Printf("Backed up previous settings to %s\n", plan.BackupPath)
	}
	fmt.Printf("Wrote %s\n", plan.Path)

	if pluginHooksInstalled() {
		fmt.Fprintf(os.Stderr, "note: the Claude Code plugin's hooks/hooks.json is present, and `init` is also\n"+
			"      writing hooks to settings.json -- that would fire every event twice.\n"+
			"      Use one registration path: uninstall the plugin's hooks, or skip `init`.\n")
	}

	// The plugin is a convenience, not a requirement: `init` works without it,
	// so a failed copy is a note, not an error. includeHooks=false because
	// settings.json already registers the hooks -- shipping them inside the
	// plugin too would make Claude Code fire every event twice.
	if dst, err := installPlugin(false, false); err == nil {
		fmt.Printf("Installed Claude Code plugin to %s (restart Claude Code, then /toolkit)\n", dst)
	} else {
		fmt.Printf("note: plugin not installed -- %v\n", err)
	}

	fmt.Printf("\nHooks are loaded when a session starts, so restart Claude Code for this to\n" +
		"take effect. Then run `claude-toolkit doctor` to verify.\n")
	return 0
}

// resolveCommand decides what command string goes into settings.json.
//
// The default pins this binary's absolute path: on macOS a GUI-launched
// Claude Code does not inherit the PATH from a shell profile, so a bare
// PATH lookup would silently disable every hook. The absolute path is stable
// for go install and install.sh layouts. --no-abs-path (usePATH) opts back
// into a bare PATH lookup, which is portable across machines at the cost of
// breaking on GUI-launched sessions.
func resolveCommand(usePATH, force bool) (string, error) {
	self, err := os.Executable()
	if err == nil {
		if resolved, rerr := filepath.EvalSymlinks(self); rerr == nil {
			self = resolved
		}
	}

	if !usePATH {
		if self != "" {
			return shellQuote(self), nil
		}
		// Could not determine our own path; fall through to PATH lookup.
		usePATH = true
	}

	found, lookErr := exec.LookPath(binName)
	if lookErr != nil {
		if !force {
			return "", fmt.Errorf(`error: %q is not on PATH, so Claude Code would not be able to run the hooks.

Pick one:
  - Add the install directory to PATH (for go install, that is %s), then re-run.
  - Or run "%s init --no-abs-path" to skip this check and write the config anyway.`,
				binName, goBinDir(), binName)
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

// injectUserStatusLine ensures ~/.claude/settings.json has a statusLine
// pointing at `claude-toolkit hud`. The plugin-directory install path
// (initSkillsScope) does not get to write this field -- the plugin
// manifest carries no statusLine concept and Claude Code reads it from
// the user-level settings.json only. This helper bridges that gap.
//
// Existing user-set statusLines are kept verbatim; the function reports
// the conflict via the install summary so the user knows to flip their
// own statusLine to use HUD.
//
// A failure here is reported as a warning, not a hard error: statusLine
// is observability, and a config we cannot parse must not strand the
// user's plugin install on a successful init.
func injectUserStatusLine(dryRun bool) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locate home: %w", err)
	}
	settingsPath := filepath.Join(home, ".claude", "settings.json")

	command, err := resolveCommand(false, false)
	if err != nil {
		// Could not pin the absolute path; fall back to the bare
		// bin name so init still completes. The hooks will not fire
		// from a GUI-launched Claude Code without inherited PATH
		// in that case, but that is the existing fall-back behaviour
		// for the legacy scope, not a new failure introduced here.
		command = binName
	}

	plan, err := installer.EnsureStatusLine(settingsPath, installer.StatusLineConfig{
		Type:    "command",
		Command: command + " hud",
	})
	if err != nil {
		return err
	}

	if plan.StatusLineKept != "" {
		fmt.Printf("  · statusLine: kept existing (%s); HUD will not render until you switch\n", plan.StatusLineKept)
		return nil
	}
	if plan.Changed() {
		fmt.Println("  + statusLine (HUD)")
	}
	if !dryRun && plan.Changed() {
		return plan.Apply()
	}
	return nil
}
