package cmd

import (
	"flag"
	"fmt"
	"os"

	"github.com/zealot00/claude-toolkit/pkg/dir"
	"github.com/zealot00/claude-toolkit/pkg/installer"
)

// uninstallCmd removes the toolkit's hook entries (symmetric to init) and,
// with --purge-config, deletes the toolkit's private directory too.
func uninstallCmd(args []string) int {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	scope := fs.String("scope", string(installer.ScopeUser), "which settings file to clean: user, project or local")
	projectDir := fs.String("project-dir", "", "project root for --scope=project|local (default: current directory)")
	purge := fs.Bool("purge-config", false, "also delete the toolkit's private directory (~/.claude-toolkit)")
	dryRun := fs.Bool("dry-run", false, "show what would change without writing anything")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s uninstall [--scope=user|project|local] [--purge-config] [--dry-run]\n\n"+
			"Removes the toolkit's hook entries from the Claude Code settings file,\n"+
			"leaving your own hooks and settings untouched. --purge-config also deletes\n"+
			"the toolkit's private state. `init --uninstall` remains as an alias.\n\nFlags:\n", binName)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fs.Usage()
		return 2
	}

	project := *projectDir
	if project == "" {
		project, _ = os.Getwd()
	}
	path, err := installer.Path(installer.Scope(*scope), project)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	plan, err := installer.BuildPlan(path, nil) // nil specs == uninstall
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if !plan.Changed() {
		fmt.Println("No claude-toolkit hooks to remove.")
	} else {
		fmt.Printf("Removing claude-toolkit hooks in %s\n%s\n", path, plan.Summary())
		if *dryRun {
			fmt.Println("\n(dry run -- nothing written)")
		} else if err := plan.Apply(); err != nil {
			fmt.Fprintf(os.Stderr, "\nerror: %v\n", err)
			return 1
		}
	}

	if *purge {
		root, err := dir.Root()
		if err == nil {
			if *dryRun {
				fmt.Printf("(dry run) would delete %s\n", root)
			} else if err := os.RemoveAll(root); err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not remove %s: %v\n", root, err)
			} else {
				fmt.Printf("Deleted %s\n", root)
			}
		}
	}

	if !*dryRun && (plan.Changed() || *purge) {
		fmt.Println("Restart Claude Code (or start a new session) for the change to take effect.")
	}
	return 0
}
