package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/zealot00/claude-toolkit/internal/hudstate"
	"github.com/zealot00/claude-toolkit/internal/profiles"
	"github.com/zealot00/claude-toolkit/internal/tokenusage"
)

// hudCmd is the statusLine command Claude Code invokes to render the HUD.
//
// The contract is small: read whatever context Claude Code passes on stdin
// (today: a JSON document with at least transcript_path), combine it with
// the persisted hudstate, and write a single ANSI-colored line to stdout.
// Claude Code displays stdout verbatim in the status line area.
//
// All failure paths degrade to "best effort" output -- never to "blank
// status line", which would be more confusing than slightly stale info.
// If anything goes wrong, the HUD prints whatever it has and exits 0 so
// Claude Code does not surface a command failure to the user.
func hudCmd(args []string) int {
	transcriptPath := readTranscriptPath(os.Stdin)
	hud, _ := hudstate.Load()
	st, _ := profiles.Load() // best effort; renderModel tolerates nil
	summary := tokenusage.Summarize(transcriptPath)
	fmt.Fprintln(os.Stdout, renderHUD(hud, summary, transcriptPath != "", st))
	return 0
}

// statusLineInput is the subset of Claude Code's statusLine payload we read.
// New fields may arrive in future versions; unknown fields are ignored so a
// schema addition cannot break the HUD.
type statusLineInput struct {
	TranscriptPath string `json:"transcript_path"`
	Cwd            string `json:"cwd"`
	Model          struct {
		DisplayName string `json:"display_name"`
	} `json:"model"`
}

// readTranscriptPath parses the statusLine payload Claude Code sends on
// stdin and returns the transcript path (empty string if absent or
// unreadable). An empty stdin is a valid input -- older Claude Code
// versions or hand-invoked smoke tests may call the command that way.
func readTranscriptPath(r io.Reader) string {
	data, err := io.ReadAll(r)
	if err != nil || len(data) == 0 {
		return ""
	}
	var in statusLineInput
	if err := json.Unmarshal(data, &in); err != nil {
		return ""
	}
	return in.TranscriptPath
}

// ANSI codes. Status lines run in Claude Code's terminal, which always
// supports colour; we don't bother with NO_COLOR detection here.
const (
	ansiReset   = "\033[0m"
	ansiDim     = "\033[2m"
	ansiGreen   = "\033[32m"
	ansiYellow  = "\033[33m"
	ansiCyan    = "\033[36m"
	ansiRed     = "\033[31m"
	ansiGray    = "\033[90m"
	filledGlyph = "●"
	emptyGlyph  = "○"
)

// renderHUD composes the status line. It is the testable core of the
// command; hudCmd exists only to wire stdin/stdout around it.
//
// hasTranscript is true when the statusLine payload included a non-empty
// transcript_path -- the caller already confirmed this so we can show the
// token count or the placeholder. st carries the provider profiles; nil is
// tolerated (the model segment is skipped).
func renderHUD(h hudstate.State, t tokenusage.Summary, hasTranscript bool, st *profiles.Store) string {
	tokens := renderTokens(t, hasTranscript)
	proxy := renderProxy(h.Proxy)
	retry := renderRetry(h.Retry)
	mode := renderMode(h.Mode)
	return join(tokens, proxy, retry, mode, renderModel(st))
}

// join concatenates any number of already-formatted segments with single
// spaces. An empty segment is skipped so a missing field does not leave a
// visible gap in the status line.
func join(parts ...string) string {
	out := ""
	for _, s := range parts {
		if s == "" {
			continue
		}
		if out != "" {
			out += " "
		}
		out += s
	}
	return out
}

func renderTokens(s tokenusage.Summary, hasTranscript bool) string {
	if !hasTranscript {
		return ansiDim + "[tok --]" + ansiReset
	}
	if s.Total == 0 {
		return ansiDim + "[tok 0]" + ansiReset
	}
	body := fmt.Sprintf(
		"[tok %s (in:%s cached:%s new:%s out:%s)]",
		formatTokens(s.Total),
		formatTokens(s.Input),
		formatTokens(s.CacheRead),
		formatTokens(s.CacheCreation),
		formatTokens(s.Output),
	)
	return ansiDim + body + ansiReset
}

// renderProxy shows port + upstream when a child is live, otherwise "OFF".
// The colour flips green -> red so a missing proxy is visually loud.
func renderProxy(p *hudstate.ProxyState) string {
	if p == nil {
		return ansiRed + "[" + emptyGlyph + " proxy OFF]" + ansiReset
	}
	body := fmt.Sprintf("[%s proxy %s:%d → %s]", filledGlyph, "127.0.0.1", p.Port, p.Upstream)
	return ansiGreen + body + ansiReset
}

// renderModel shows the active provider segment: green when the settings env
// matches a stored profile, yellow when the env points at a provider we do
// not have a profile for, and nothing when no ANTHROPIC_BASE_URL is set
// (Claude Code is on its default Anthropic auth).
func renderModel(st *profiles.Store) string {
	base := settingsEnvBaseURL()
	if base == "" || st == nil {
		return ""
	}
	if active := activeProfileName(st); active != "" {
		p := st.Profiles[active]
		return ansiGreen + fmt.Sprintf("[%s %s %s]", filledGlyph, active, shortModel(p["ANTHROPIC_MODEL"])) + ansiReset
	}
	return ansiYellow + fmt.Sprintf("[%s custom %s]", emptyGlyph, truncateHost(hostOf(base))) + ansiReset
}

// shortModel trims a long model id for the narrow status line.
func shortModel(m string) string {
	if len(m) <= 24 {
		return m
	}
	return m[:21] + "..."
}

// truncateHost keeps the status line narrow for custom providers.
func truncateHost(h string) string {
	if len(h) <= 32 {
		return h
	}
	return h[:29] + "..."
}

// renderRetry picks a colour by retry state so the operator can read the
// HUD at a glance: yellow HOOK = "Claude Code is being held back", green
// PROXY = "transport layer is handling 429s", grey OFF = "neither engaged".
func renderRetry(state string) string {
	body := ""
	color := ansiGray
	switch state {
	case hudstate.RetryHook:
		body = fmt.Sprintf("[%s retry HOOK]", filledGlyph)
		color = ansiYellow
	case hudstate.RetryProxy:
		body = fmt.Sprintf("[%s retry PROXY]", filledGlyph)
		color = ansiGreen
	default:
		body = fmt.Sprintf("[%s retry OFF]", emptyGlyph)
	}
	return color + body + ansiReset
}

// renderMode shows the permission mode Claude Code is running in. Empty
// mode (no session has started yet) renders as a grey placeholder so the
// gap is not invisible.
func renderMode(mode string) string {
	if mode == "" {
		return ansiGray + "[" + emptyGlyph + " mode --]" + ansiReset
	}
	return ansiCyan + fmt.Sprintf("[%s mode %s]", filledGlyph, mode) + ansiReset
}

// formatTokens renders a token count compactly: 1234 -> "1.2k", 12345 ->
// "12k", 1_500_000 -> "1.5M". Anything under 1000 stays in raw form so a
// fresh session does not need a unit suffix.
func formatTokens(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d", n)
	case n < 10_000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	case n < 1_000_000:
		return fmt.Sprintf("%dk", n/1000)
	case n < 10_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	default:
		return fmt.Sprintf("%dM", n/1_000_000)
	}
}
