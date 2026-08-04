// Package tokenusage computes a session's cumulative token consumption by
// streaming the JSONL transcript that Claude Code writes for each session.
//
// Why streaming: a long session's transcript can be megabytes. Reading it all
// into memory just to discard the lines we don't care about (every line but
// assistant turns) would be wasteful and would make the HUD command lag on
// large histories. The Summarize function scans line by line and ignores any
// line that is not an assistant message with a usage block.
//
// Fail-open: a missing or unreadable transcript returns the zero Summary so
// the HUD command can still render the rest of its status row.
package tokenusage

import (
	"bufio"
	"encoding/json"
	"os"
)

// Summary is the accumulated token usage for one session.
//
// The four sub-counts are tracked separately so the HUD can show where the
// tokens are coming from (cache reads are cheap, fresh input is expensive).
// Total is the sum of all four.
type Summary struct {
	Input         int
	CacheCreation int
	CacheRead     int
	Output        int
	Total         int
}

// transcriptLine is the subset of Claude Code's transcript entry shape we
// need. The full entry carries many other fields (type, parentUuid, etc.)
// that we deliberately ignore to keep this parse cheap.
type transcriptLine struct {
	Type    string `json:"type"`
	Message struct {
		Usage struct {
			InputTokens              int `json:"input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			OutputTokens             int `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// Summarize reads the JSONL transcript at path line-by-line and returns the
// accumulated usage. Returns the zero Summary on any error (file missing,
// unreadable, every line corrupt) so the HUD command can still run.
//
// An empty path or an empty file is not an error -- the zero Summary is the
// correct answer in both cases.
func Summarize(path string) Summary {
	if path == "" {
		return Summary{}
	}
	f, err := os.Open(path)
	if err != nil {
		return Summary{}
	}
	defer f.Close()

	var s Summary
	scanner := bufio.NewScanner(f)
	// Claude Code transcripts are line-delimited JSON; default 64 KiB scan
	// buffer is plenty for any one line. A pathologically huge line would
	// hit ErrScanTooLong and we drop it (the line probably isn't usage
	// anyway).
	for scanner.Scan() {
		var line transcriptLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			continue // skip corrupt lines; fail-open
		}
		if line.Type != "assistant" {
			continue
		}
		u := line.Message.Usage
		s.Input += u.InputTokens
		s.CacheCreation += u.CacheCreationInputTokens
		s.CacheRead += u.CacheReadInputTokens
		s.Output += u.OutputTokens
	}
	s.Total = s.Input + s.CacheCreation + s.CacheRead + s.Output
	return s
}
