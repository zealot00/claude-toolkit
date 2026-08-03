package hooks

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/zealot00/claude-toolkit/internal/dispatcher"
	"github.com/zealot00/claude-toolkit/internal/payload"
)

// Guard is the "guard" capability. It is a PreToolUse hook that blocks
// irreversible or exfiltrating commands before Claude Code runs them.
//
// It is deliberately narrow. Every rule targets an action that destroys data
// outside the working tree, hands a shell to a remote server, or leaks a
// credential -- things no ordinary development command does. `rm -rf ./build`
// and `git push --force-with-lease` pass untouched, because a guard that cries
// wolf gets uninstalled.
func Guard() *dispatcher.Route {
	return &dispatcher.Route{
		Name:    "guard",
		Events:  []string{payload.EventPreToolUse},
		Tools:   []string{"Bash", "Write", "Edit", "NotebookEdit"},
		Handler: guard,
	}
}

func guard(_ context.Context, e *payload.Event) (*payload.Response, error) {
	if e.ToolName == "Bash" {
		in, err := e.Bash()
		if err != nil {
			return nil, err
		}
		return decide(checkBash(in.Command)), nil
	}
	in, err := e.File()
	if err != nil {
		return nil, err
	}
	return decide(checkPath(in.Path())), nil
}

// finding is one rule match.
type finding struct {
	rule     string
	decision string // payload.DecisionDeny or payload.DecisionAsk
	reason   string
}

// decide folds findings into a response, worst-first. Nil when nothing matched,
// which lets the normal permission flow proceed untouched.
func decide(fs []finding) *payload.Response {
	var deny, ask []finding
	for _, f := range fs {
		if f.decision == payload.DecisionDeny {
			deny = append(deny, f)
		} else {
			ask = append(ask, f)
		}
	}
	if len(deny) > 0 {
		return payload.Deny(render("Blocked by claude-toolkit guard", deny))
	}
	if len(ask) > 0 {
		return payload.Ask(render("claude-toolkit guard wants confirmation", ask))
	}
	return nil
}

func render(header string, fs []finding) string {
	var b strings.Builder
	b.WriteString(header)
	b.WriteString(":\n")
	for _, f := range fs {
		fmt.Fprintf(&b, "  - [%s] %s\n", f.rule, f.reason)
	}
	return strings.TrimRight(b.String(), "\n")
}

// forkBomb matches the classic :(){ :|:& };: and its whitespace variants.
var forkBomb = regexp.MustCompile(`(?s)([\w:]+)\s*\(\s*\)\s*\{.*\|.*&.*\}`)

// downloaders fetch remote content; shells execute whatever they are handed.
// A pipe from the former to the latter is remote code execution.
var (
	downloaders = map[string]bool{"curl": true, "wget": true, "fetch": true, "aria2c": true, "httpie": true, "http": true}
	shells      = map[string]bool{
		"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "fish": true,
		"python": true, "python3": true, "node": true, "perl": true, "ruby": true, "php": true,
	}
	// exfilSinks send data off the machine.
	exfilSinks = map[string]bool{"curl": true, "wget": true, "nc": true, "ncat": true, "netcat": true, "ssh": true, "scp": true}
)

// dangerousRoots are paths whose recursive deletion is never a development
// action. Entries are compared after stripping a trailing "/" or "/*".
var dangerousRoots = map[string]bool{
	"/": true, "~": true, "$HOME": true, "${HOME}": true,
	"/Users": true, "/home": true, "/etc": true, "/var": true, "/usr": true,
	"/bin": true, "/sbin": true, "/lib": true, "/opt": true, "/boot": true,
	"/dev": true, "/proc": true, "/sys": true, "/System": true, "/Library": true,
	"/Applications": true, "*": true, ".": true,
}

// secretFiles are credential stores. Reading one into a network sink is
// exfiltration; writing one is usually a mistake.
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(^|/)\.ssh/id_[\w]+$`),
	regexp.MustCompile(`(^|/)\.aws/credentials$`),
	regexp.MustCompile(`(^|/)\.netrc$`),
	regexp.MustCompile(`(^|/)\.npmrc$`),
	regexp.MustCompile(`(^|/)id_(rsa|dsa|ecdsa|ed25519)$`),
	regexp.MustCompile(`(^|/)\.claude/settings\.json$`),
	regexp.MustCompile(`(^|/)\.env(\.[\w.]+)?$`),
	regexp.MustCompile(`(^|/)\.git-credentials$`),
	regexp.MustCompile(`\.(pem|p12|pfx|keystore)$`),
}

func isSecretPath(p string) bool {
	p = strings.Trim(p, `"'`)
	for _, re := range secretPatterns {
		if re.MatchString(p) {
			return true
		}
	}
	return false
}

// normTarget strips quotes and a trailing glob so "/*" and "/" compare equal.
func normTarget(t string) string {
	t = strings.Trim(t, `"'`)
	t = strings.TrimSuffix(t, "/*")
	t = strings.TrimSuffix(t, "/.")
	if len(t) > 1 {
		t = strings.TrimSuffix(t, "/")
	}
	if t == "" {
		// The input was "/*" or "/": both name the filesystem root.
		return "/"
	}
	return t
}

// flagSet collects the short flags (bundled or separate) and long flags
// present in an operand list, so rules can test for them without re-parsing.
func flagSet(operands []string) (short map[rune]bool, long map[string]bool) {
	short, long = map[rune]bool{}, map[string]bool{}
	for _, o := range operands {
		if !strings.HasPrefix(o, "-") || o == "-" {
			continue
		}
		if name, ok := strings.CutPrefix(o, "--"); ok {
			if eq := strings.IndexByte(name, '='); eq >= 0 {
				name = name[:eq]
			}
			long[name] = true
			continue
		}
		for _, r := range o[1:] {
			short[r] = true
		}
	}
	return short, long
}

// isRecursiveForce reports an rm that is both recursive and forced, accepting
// -r, -R, --recursive for the first and -f, --force for the second.
func isRecursiveForce(operands []string) bool {
	short, long := flagSet(operands)
	recursive := short['r'] || short['R'] || long["recursive"]
	force := short['f'] || long["force"]
	return recursive && force
}

// isRecursive reports a plain recursive flag, as chmod spells it.
func isRecursive(operands []string) bool {
	short, long := flagSet(operands)
	return short['R'] || short['r'] || long["recursive"]
}

func operandsOnly(operands []string) []string {
	var out []string
	for _, o := range operands {
		if strings.HasPrefix(o, "-") && o != "-" {
			continue
		}
		out = append(out, o)
	}
	return out
}

// checkBash applies every rule to a shell command line.
func checkBash(cmd string) []finding {
	var fs []finding
	if strings.TrimSpace(cmd) == "" {
		return nil
	}

	if forkBomb.MatchString(cmd) {
		fs = append(fs, finding{"fork-bomb", payload.DecisionDeny,
			"this defines a self-replicating function that will exhaust process slots"})
	}

	segs := tokenize(cmd)
	for i, s := range segs {
		b, operands, ok := s.base()
		if !ok {
			b = ""
		}

		// Remote code execution: downloader piped into an interpreter.
		if ok && downloaders[b] && i+1 < len(segs) && segs[i+1].pipedFrom {
			if nb, _, nok := segs[i+1].base(); nok && shells[nb] {
				fs = append(fs, finding{"pipe-to-shell", payload.DecisionDeny,
					fmt.Sprintf("piping %s output straight into %s executes unreviewed remote code; download to a file and read it first", b, nb)})
			}
		}

		// Credential exfiltration: a secret path anywhere in a pipeline that
		// ends at a network sink.
		if ok {
			for _, f := range s.fields {
				if !isSecretPath(f) {
					continue
				}
				if sinkInPipeline(segs, i) {
					fs = append(fs, finding{"secret-exfil", payload.DecisionDeny,
						fmt.Sprintf("%s is a credential file and this pipeline sends it to a network destination", f)})
					break
				}
			}
		}

		// Writing to a raw block device destroys the partition table.
		for _, rd := range s.redirects {
			if strings.HasPrefix(normTarget(rd), "/dev/") && !isHarmlessDevice(normTarget(rd)) {
				fs = append(fs, finding{"block-device-write", payload.DecisionDeny,
					fmt.Sprintf("redirecting into %s writes directly to a device node", rd)})
			}
		}

		if !ok {
			continue
		}

		switch {
		case b == "rm" && isRecursiveForce(operands):
			for _, t := range operandsOnly(operands) {
				if dangerousRoots[normTarget(t)] {
					fs = append(fs, finding{"rm-rf-root", payload.DecisionDeny,
						fmt.Sprintf("recursive force-delete of %q removes data outside the working tree and cannot be undone", t)})
				}
			}

		case b == "dd":
			for _, o := range operands {
				if strings.HasPrefix(o, "of=/dev/") && !isHarmlessDevice(strings.TrimPrefix(o, "of=")) {
					fs = append(fs, finding{"dd-to-device", payload.DecisionDeny,
						fmt.Sprintf("dd %s overwrites a raw device", o)})
				}
			}

		case strings.HasPrefix(b, "mkfs"):
			fs = append(fs, finding{"mkfs", payload.DecisionDeny,
				"creating a filesystem erases everything on the target device"})

		case b == "chmod" && isRecursive(operands):
			for _, t := range operandsOnly(operands) {
				if t == "777" || t == "-R" || strings.Contains(t, "rwx") {
					continue
				}
				if dangerousRoots[normTarget(t)] && containsMode(operands, "777", "a+rwx") {
					fs = append(fs, finding{"chmod-world-writable", payload.DecisionDeny,
						fmt.Sprintf("recursively making %s world-writable breaks system security", t)})
				}
			}

		case b == "git" && contains(operands, "push") && forcePush(operands):
			fs = append(fs, finding{"git-force-push", payload.DecisionAsk,
				"force-push rewrites published history; use --force-with-lease if you meant to do this"})

		case b == "shutdown" || b == "reboot" || b == "halt":
			fs = append(fs, finding{"power-state", payload.DecisionAsk,
				fmt.Sprintf("%s ends the session and any unsaved work", b)})
		}
	}
	return fs
}

// sinkInPipeline reports whether the pipeline containing segment i eventually
// reaches a command that can send data off the machine.
func sinkInPipeline(segs []segment, i int) bool {
	if b, _, ok := segs[i].base(); ok && exfilSinks[b] {
		return true
	}
	for j := i + 1; j < len(segs) && segs[j].pipedFrom; j++ {
		if b, _, ok := segs[j].base(); ok && exfilSinks[b] {
			return true
		}
	}
	return false
}

// isHarmlessDevice exempts the device nodes that scripts legitimately use.
func isHarmlessDevice(p string) bool {
	switch p {
	case "/dev/null", "/dev/stdout", "/dev/stderr", "/dev/stdin", "/dev/tty", "/dev/zero", "/dev/urandom", "/dev/random":
		return true
	}
	return false
}

func containsMode(operands []string, modes ...string) bool {
	for _, m := range modes {
		if slices.Contains(operands, m) {
			return true
		}
	}
	return false
}

func contains(ss []string, want string) bool {
	return slices.Contains(ss, want)
}

// forcePush distinguishes --force from the safe --force-with-lease.
func forcePush(operands []string) bool {
	for _, o := range operands {
		if strings.HasPrefix(o, "--force-with-lease") || strings.HasPrefix(o, "--force-if-includes") {
			return false
		}
	}
	for _, o := range operands {
		if o == "--force" || o == "-f" {
			return true
		}
		if strings.HasPrefix(o, "-") && !strings.HasPrefix(o, "--") && strings.Contains(o, "f") {
			return true
		}
	}
	return false
}

// checkPath guards Write/Edit against credential files.
func checkPath(p string) []finding {
	if p == "" {
		return nil
	}
	if !isSecretPath(p) {
		return nil
	}
	// Private keys and cloud credentials are never a legitimate edit target.
	base := path.Base(p)
	if strings.HasPrefix(base, "id_") || base == "credentials" || base == ".netrc" || base == ".git-credentials" {
		return []finding{{"write-to-secret", payload.DecisionDeny,
			fmt.Sprintf("%s holds private credentials and must not be written by an agent", p)}}
	}
	// .env, .npmrc and settings.json are edited legitimately often enough that
	// a confirmation prompt beats a hard block.
	return []finding{{"write-to-secret", payload.DecisionAsk,
		fmt.Sprintf("%s can contain secrets -- confirm this write is intended", p)}}
}
