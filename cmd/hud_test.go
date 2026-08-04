package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zealot00/claude-toolkit/internal/hudstate"
	"github.com/zealot00/claude-toolkit/internal/tokenusage"
	"github.com/zealot00/claude-toolkit/pkg/dir"
)

func TestRenderHUD_AllFieldsPresent(t *testing.T) {
	t.Setenv(dir.EnvHome, t.TempDir())

	hud := hudstate.State{
		Proxy: &hudstate.ProxyState{Port: 8080, Upstream: "https://api.moonshot.cn/anthropic"},
		Retry: hudstate.RetryProxy,
		Mode:  "bypassPermissions",
	}
	summary := tokenusage.Summary{
		Input: 30000, CacheRead: 10000, CacheCreation: 0, Output: 2000, Total: 42000,
	}
	out := renderHUD(hud, summary, true)
	want := []string{
		"30k",  // input
		"10k",  // cached
		"42k",  // total
		"8080", // proxy port
		"moonshot",
		"PROXY",
		"bypassPermissions",
		ansiReset,
	}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("renderHUD missing %q\nfull output: %s", w, out)
		}
	}
}

func TestRenderHUD_NoHudStateShowsOnlyTokens(t *testing.T) {
	out := renderHUD(hudstate.State{}, tokenusage.Summary{Total: 1500}, true)
	if !strings.Contains(out, "[tok 1.5k") {
		t.Errorf("expected token segment, got %q", out)
	}
	// No proxy, no retry, no mode set: should render OFF / -- placeholders,
	// not disappear entirely.
	if !strings.Contains(out, "proxy OFF") {
		t.Errorf("expected 'proxy OFF' placeholder, got %q", out)
	}
	if !strings.Contains(out, "retry OFF") {
		t.Errorf("expected 'retry OFF' placeholder, got %q", out)
	}
	if !strings.Contains(out, "mode --") {
		t.Errorf("expected 'mode --' placeholder, got %q", out)
	}
}

func TestRenderHUD_NoTranscriptPathShowsDashDash(t *testing.T) {
	out := renderHUD(hudstate.State{Mode: "default"}, tokenusage.Summary{}, false)
	if !strings.Contains(out, "[tok --]") {
		t.Errorf("expected [tok --] placeholder, got %q", out)
	}
	if !strings.Contains(out, "default") {
		t.Errorf("mode segment should still render, got %q", out)
	}
}

func TestRenderHUD_EmptySummaryShowsZero(t *testing.T) {
	out := renderHUD(hudstate.State{}, tokenusage.Summary{}, true)
	if !strings.Contains(out, "[tok 0]") {
		t.Errorf("expected [tok 0] for fresh transcript, got %q", out)
	}
}

func TestRenderHUD_AllRetryStatesUseDistinctGlyphAndColor(t *testing.T) {
	cases := []struct {
		state    string
		glyph    string
		wantText string
	}{
		{hudstate.RetryHook, filledGlyph, "HOOK"},
		{hudstate.RetryProxy, filledGlyph, "PROXY"},
		{hudstate.RetryOff, emptyGlyph, "OFF"},
		{"", emptyGlyph, "OFF"},
	}
	for _, c := range cases {
		out := renderRetry(c.state)
		if !strings.Contains(out, c.wantText) {
			t.Errorf("retry %q: missing %q, got %q", c.state, c.wantText, out)
		}
		if !strings.Contains(out, c.glyph) {
			t.Errorf("retry %q: missing glyph %q, got %q", c.state, c.glyph, out)
		}
	}
}

func TestRenderProxy_AllBranches(t *testing.T) {
	if out := renderProxy(nil); !strings.Contains(out, "OFF") {
		t.Errorf("nil proxy should show OFF, got %q", out)
	}
	p := &hudstate.ProxyState{Port: 8080, Upstream: "https://x"}
	out := renderProxy(p)
	if !strings.Contains(out, "8080") || !strings.Contains(out, "https://x") {
		t.Errorf("expected port and upstream, got %q", out)
	}
}

func TestFormatTokens(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1.0k"},
		{1234, "1.2k"},
		{9999, "10.0k"},
		{12345, "12k"},
		{1_500_000, "1.5M"},
		{12_000_000, "12M"},
	}
	for _, c := range cases {
		if got := formatTokens(c.in); got != c.want {
			t.Errorf("formatTokens(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestReadTranscriptPath_ValidJSON(t *testing.T) {
	r := strings.NewReader(`{"transcript_path":"/tmp/x.jsonl","cwd":"/tmp"}`)
	if got := readTranscriptPath(r); got != "/tmp/x.jsonl" {
		t.Errorf("got %q", got)
	}
}

func TestReadTranscriptPath_EmptyInput(t *testing.T) {
	if got := readTranscriptPath(strings.NewReader("")); got != "" {
		t.Errorf("empty input should yield empty string, got %q", got)
	}
}

func TestReadTranscriptPath_InvalidJSON(t *testing.T) {
	if got := readTranscriptPath(strings.NewReader("{not valid")); got != "" {
		t.Errorf("invalid JSON should yield empty string, got %q", got)
	}
}

func TestHudCmd_WritesRenderedLineToStdout(t *testing.T) {
	t.Setenv(dir.EnvHome, t.TempDir())

	// Pre-populate hudstate so the output contains all four segments.
	if err := hudstate.Save(hudstate.State{
		Proxy: &hudstate.ProxyState{Port: 8080, Upstream: "https://api.anthropic.com"},
		Retry: hudstate.RetryProxy,
		Mode:  "bypassPermissions",
	}); err != nil {
		t.Fatal(err)
	}

	// Lay down a transcript so the token field has real numbers.
	transcript := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(transcript, []byte(
		`{"type":"assistant","message":{"usage":{"input_tokens":100,"output_tokens":20}}}`+"\n"+
			`{"type":"assistant","message":{"usage":{"input_tokens":150,"cache_read_input_tokens":40,"output_tokens":25}}}`+"\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}

	stdin := strings.NewReader(`{"transcript_path":"` + transcript + `"}`)
	var stdout bytes.Buffer

	// Run the testable core by calling the format fn directly with the
	// parsed transcript path -- we already test the parser separately.
	transcriptPath := readTranscriptPath(stdin)
	hud, _ := hudstate.Load()
	summary := tokenusage.Summarize(transcriptPath)
	stdout.WriteString(renderHUD(hud, summary, transcriptPath != "") + "\n")

	if !strings.Contains(stdout.String(), "[tok ") {
		t.Errorf("expected token segment, got %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "127.0.0.1:8080") {
		t.Errorf("expected proxy 127.0.0.1:8080, got %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "PROXY") {
		t.Errorf("expected PROXY, got %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "bypassPermissions") {
		t.Errorf("expected mode bypassPermissions, got %s", stdout.String())
	}
}
