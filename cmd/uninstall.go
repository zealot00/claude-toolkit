package cmd

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zealot00/claude-toolkit/pkg/dir"
	"github.com/zealot00/claude-toolkit/pkg/installer"
)

// uninstallCmd removes the toolkit's hook entries (symmetric to init) and,
// with --purge-config, deletes the toolkit's private directory too.
//
// skills-user / skills-project scopes remove the plugin directory Claude Code
// auto-loads, instead of (or in addition to) editing settings.json.
func uninstallCmd(args []string) int {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	scope := fs.String("scope", string(installer.ScopeUser), "which settings file to clean: user, project, local, skills-user, or skills-project")
	projectDir := fs.String("project-dir", "", "project root for --scope=project|local|skills-project (default: current directory)")
	purge := fs.Bool("purge-config", false, "also delete the toolkit's private directory (~/.claude-toolkit)")
	dryRun := fs.Bool("dry-run", false, "show what would change without writing anything")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s uninstall [--scope=user|project|local|skills-user|skills-project] [--purge-config] [--dry-run]\n\n"+
			"Removes the toolkit's hook entries from the Claude Code settings file,\n"+
			"leaving your own hooks and settings untouched. skills-user/skills-project\n"+
			"remove the auto-loading plugin directory instead. --purge-config also\n"+
			"deletes the toolkit's private state. `init --uninstall` is an alias.\n\nFlags:\n", binName)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fs.Usage()
		return 2
	}

	// skills-* scopes: remove the plugin directory and (with --purge-config)
	// the private state. They never touch settings.json.
	if *scope == "skills-user" || *scope == "skills-project" {
		project := *projectDir
		if project == "" {
			project, _ = os.Getwd()
		}
		var dst string
		if *scope == "skills-user" {
			home, err := os.UserHomeDir()
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return 1
			}
			dst = filepath.Join(home, ".claude", "skills", "claude-toolkit")
		} else {
			dst = filepath.Join(project, ".claude", "skills", "claude-toolkit")
		}
		if _, err := os.Stat(dst); os.IsNotExist(err) {
			fmt.Printf("No claude-toolkit plugin at %s; nothing to remove.\n", dst)
		} else {
			fmt.Printf("Removing claude-toolkit plugin at %s\n", dst)
			if *dryRun {
				fmt.Println("(dry run -- nothing removed)")
			} else if err := os.RemoveAll(dst); err != nil {
				fmt.Fprintf(os.Stderr, "error: remove %s: %v\n", dst, err)
				return 1
			} else {
				fmt.Printf("Removed %s\n", dst)
			}
		}
		if *purge {
			if root, err := dir.Root(); err == nil {
				if *dryRun {
					fmt.Printf("(dry run) would delete %s\n", root)
				} else if err := os.RemoveAll(root); err != nil {
					fmt.Fprintf(os.Stderr, "warning: could not remove %s: %v\n", root, err)
				} else {
					fmt.Printf("Deleted %s\n", root)
				}
			}
		}
		if !*dryRun {
			fmt.Println("Restart Claude Code (or start a new session) for the change to take effect.")
		}
		return 0
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
