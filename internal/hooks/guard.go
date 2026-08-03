package hooks

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path"
	"path/filepath"
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

func guard(ctx context.Context, e *payload.Event) (*payload.Response, error) {
	if e.ToolName == "Bash" {
		in, err := e.Bash()
		if err != nil {
			return nil, err
		}
		fs := checkBash(in.Command)
		if gitWriteToProtectedBranch(ctx, e.Cwd, in.Command) {
			fs = append(fs, finding{"protected-branch", payload.DecisionAsk,
				"this repository's current branch is protected; a write here may bypass review"})
		}
		return decide(fs), nil
	}
	in, err := e.File()
	if err != nil {
		return nil, err
	}
	fs := checkPath(in.Path())
	if in.Content != "" {
		fs = append(fs, checkContent(in.Content)...)
	}
	if in.NewString != "" {
		fs = append(fs, checkContent(in.NewString)...)
	}
	return decide(fs), nil
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
	regexp.MustCompile(`(^|[/\\])\.ssh/id_[\w]+$`),
	regexp.MustCompile(`(^|[/\\])\.aws/credentials$`),
	regexp.MustCompile(`(^|[/\\])\.netrc$`),
	regexp.MustCompile(`(^|[/\\])\.npmrc$`),
	regexp.MustCompile(`(^|[/\\])id_(rsa|dsa|ecdsa|ed25519)$`),
	regexp.MustCompile(`(^|[/\\])\.claude/settings\.json$`),
	regexp.MustCompile(`(^|[/\\])\.env(\.[\w.]+)?$`),
	regexp.MustCompile(`(^|[/\\])\.git-credentials$`),
	regexp.MustCompile(`\.(pem|p12|pfx|keystore)$`),
}

func isSecretPath(p string) bool {
	p = strings.ReplaceAll(strings.Trim(p, `"'`), `\`, "/")
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

		// Reading an unbounded log stream dumps megabytes into the transcript.
		// Offer the bounded alternative instead of blocking outright.
		if isLogDump(b, operands) {
			fs = append(fs, finding{"log-dump", payload.DecisionAsk,
				fmt.Sprintf("%s may stream a huge log; use a bounded view (e.g. tail -100, journalctl --since=10m) instead", b)})
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

		case b == "git" && contains(operands, "reset") && contains(operands, "--hard"):
			fs = append(fs, finding{"git-reset-hard", payload.DecisionDeny,
				"git reset --hard discards uncommitted work and cannot be undone"})

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

// secretPatterns match credential-shaped strings. Entropy is judged on the
// token's variable body (submatch bodyIndex), not the fixed prefix (AKIA,
// ghp_, sk-...) which dilutes it. The gate is per-pattern because the body
// length caps achievable entropy: 16 distinct characters max out at 4.0
// bits/char, so an AWS key can never reach a 4.5 gate that a 36-character
// GitHub token clears easily. PEM markers are declarations, not random
// material, so they bypass the entropy gate entirely.
type secretPattern struct {
	re         *regexp.Regexp
	bodyIndex  int // submatch index of the variable part; 0 = whole match
	minEntropy float64
}

var secretTokenPatterns = []secretPattern{
	{regexp.MustCompile(`AKIA([0-9A-Z]{16})`), 1, 3.5},
	{regexp.MustCompile(`gh[pousr]_([A-Za-z0-9]{20,})`), 1, 4.5},
	{regexp.MustCompile(`sk-([A-Za-z0-9]{20,})`), 1, 4.5},
	{regexp.MustCompile(`xox[baprs]-([A-Za-z0-9-]{10,})`), 1, 4.5},
	{regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`), 0, 0},
}

// checkContent scans Write/Edit content for credential-shaped tokens with
// high entropy, denying the write so a real secret cannot be committed.
func checkContent(content string) []finding {
	if content == "" {
		return nil
	}
	var fs []finding
	seen := map[string]bool{}
	for _, pat := range secretTokenPatterns {
		for _, m := range pat.re.FindAllStringSubmatch(content, -1) {
			whole := m[0]
			if seen[whole] {
				continue
			}
			seen[whole] = true

			body := whole
			if pat.bodyIndex > 0 && pat.bodyIndex < len(m) {
				body = m[pat.bodyIndex]
			}
			if len(body) < 12 {
				continue
			}
			h := shannonEntropy(body)
			if pat.minEntropy > 0 && h < pat.minEntropy {
				continue
			}
			fs = append(fs, finding{"high-entropy-secret", payload.DecisionDeny,
				fmt.Sprintf("content contains a credential-shaped token (%s, entropy %.2f); confirm this is not a real secret before writing it", maskToken(whole), h)})
		}
	}
	return fs
}

// shannonEntropy computes the per-character Shannon entropy of s.
func shannonEntropy(s string) float64 {
	if s == "" {
		return 0
	}
	counts := make([]int, 256)
	for i := 0; i < len(s); i++ {
		counts[s[i]]++
	}
	var h float64
	for _, c := range counts {
		if c == 0 {
			continue
		}
		p := float64(c) / float64(len(s))
		h -= p * math.Log2(p)
	}
	return h
}

// maskToken shows only the head and tail of a suspected secret in messages.
func maskToken(s string) string {
	if len(s) <= 8 {
		return "***"
	}
	return s[:4] + "..." + s[len(s)-4:]
}

// isLogDump reports a command that can stream unbounded log output: cat of a
// log path, journalctl without a time/line bound, or kubectl logs.
func isLogDump(b string, operands []string) bool {
	switch b {
	case "cat", "less", "more", "zcat":
		for _, o := range operandsOnly(operands) {
			if strings.Contains(o, "/var/log/") || strings.HasSuffix(o, ".log") {
				return true
			}
		}
	case "journalctl":
		for _, o := range operands {
			if strings.HasPrefix(o, "--since") || o == "-S" || strings.HasPrefix(o, "--lines") || o == "-n" {
				return false
			}
		}
		return true
	case "kubectl":
		return contains(operands, "logs")
	}
	return false
}

// protectedBranches are the branch names a guard prompt should appear for.
var protectedBranches = []string{"main", "master", "release", "production"}

// gitWriteToProtectedBranch reports whether cmd is a git commit/push on the
// repo's current branch while that branch is protected (unless the project
// opts out with a .claude-toolkit-allow marker).
func gitWriteToProtectedBranch(ctx context.Context, dir, cmd string) bool {
	if !isGitWrite(cmd) {
		return false
	}
	if branchAllowedByWhitelist(dir) {
		return false
	}
	branch := currentBranch(ctx, dir)
	for _, b := range protectedBranches {
		if branch == b || strings.HasPrefix(branch, b+"/") || strings.HasPrefix(branch, "release/") {
			return true
		}
	}
	return false
}

// isGitWrite reports whether cmd is a git commit or push.
func isGitWrite(cmd string) bool {
	for _, s := range tokenize(cmd) {
		b, operands, ok := s.base()
		if ok && b == "git" && (contains(operands, "commit") || contains(operands, "push")) {
			return true
		}
	}
	return false
}

// branchAllowedByWhitelist walks up from dir looking for a
// .claude-toolkit-allow marker that opts the project out of branch guards.
func branchAllowedByWhitelist(dir string) bool {
	for d := dir; d != ""; {
		if _, err := os.Stat(filepath.Join(d, ".claude-toolkit-allow")); err == nil {
			return true
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	return false
}

func currentBranch(ctx context.Context, dir string) string {
	if dir == "" {
		return ""
	}
	cmd := exec.CommandContext(ctx, "git", "branch", "--show-current")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
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
	// Normalize separators first so Windows paths (C:\Users\...) hit the same
	// verdicts as POSIX ones. filepath.ToSlash does NOT do this on POSIX hosts
	// (backslash is not a separator there), so replace it manually.
	base := path.Base(strings.ReplaceAll(p, `\`, "/"))
	if strings.HasPrefix(base, "id_") || base == "credentials" || base == ".netrc" || base == ".git-credentials" {
		return []finding{{"write-to-secret", payload.DecisionDeny,
			fmt.Sprintf("%s holds private credentials and must not be written by an agent", p)}}
	}
	// .env, .npmrc and settings.json are edited legitimately often enough that
	// a confirmation prompt beats a hard block.
	return []finding{{"write-to-secret", payload.DecisionAsk,
		fmt.Sprintf("%s can contain secrets -- confirm this write is intended", p)}}
}
