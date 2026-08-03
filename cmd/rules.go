package cmd

import (
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
)

// ruleInfo documents one built-in rule for `claude-toolkit rules`. The list
// mirrors what the guard/heal capabilities actually enforce; keep it in sync
// when rules change.
type ruleInfo struct {
	rule     string
	decision string
	what     string
}

var builtinRules = []ruleInfo{
	{"rm-rf-root", "deny", "recursive force-delete of /, ~, $HOME, /usr, /etc, ..."},
	{"pipe-to-shell", "deny", "curl | sh / wget | bash / curl | python"},
	{"secret-exfil", "deny", "credential file piped to a network sink (curl, nc, ssh, ...)"},
	{"dd-to-device", "deny", "dd of=/dev/... writing a raw device"},
	{"mkfs", "deny", "any mkfs* invocation"},
	{"block-device-write", "deny", "redirecting into a raw device node"},
	{"fork-bomb", "deny", "self-replicating shell function"},
	{"git-reset-hard", "deny", "git reset --hard discards uncommitted work"},
	{"write-to-secret", "deny", "writing ~/.ssh keys, ~/.aws/credentials, .netrc"},
	{"write-to-secret", "ask", "writing .env, .npmrc, *.pem, ~/.claude/settings.json"},
	{"high-entropy-secret", "deny", "credential-shaped token (AKIA/ghp_/sk-/PEM) with high entropy in Write/Edit content"},
	{"protected-branch", "ask", "git commit/push on main/master/release/production (opt out with .claude-toolkit-allow)"},
	{"git-force-push", "ask", "git push --force (not --force-with-lease)"},
	{"power-state", "ask", "shutdown / reboot / halt"},
	{"chmod-world-writable", "deny", "recursive chmod 777 on a system root"},
	{"log-dump", "ask", "cat of log paths, unbounded journalctl, kubectl logs -- suggests a bounded view"},
	{"bash-loop", "deny", "same Bash command failing 3+ consecutive times (loopguard)"},
}

// rulesCmd lists and explains the toolkit's built-in rules.
func rulesCmd(args []string) int {
	fs := flag.NewFlagSet("rules", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s rules\n\nLists every built-in rule with its verdict.\n", binName)
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fs.Usage()
		return 2
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "rule\tdecision\twhat")
	for _, r := range builtinRules {
		fmt.Fprintf(w, "%s\t%s\t%s\n", r.rule, r.decision, r.what)
	}
	w.Flush()
	fmt.Printf("\n%d rule(s). Rules ship with the binary; there is no separate rule file to edit.\n", len(builtinRules))
	return 0
}
