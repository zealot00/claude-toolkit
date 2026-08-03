package cmd

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/zealot00/claude-toolkit/internal/capcfg"
	"github.com/zealot00/claude-toolkit/internal/hooks"
	"github.com/zealot00/claude-toolkit/internal/payload"
)

// runCmd is the entry point Claude Code invokes. It reads one event JSON
// document from stdin and writes at most one response JSON document to stdout.
//
// It always exits 0. Claude Code treats a non-zero exit as a hook failure and
// surfaces it in the transcript, and exit 2 blocks the tool outright -- so a
// bug in this binary would either spam the user or wedge their session. Every
// error path here degrades to "no opinion" and logs instead. Blocking is only
// ever expressed through a deliberate JSON deny.
func runCmd(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	event := fs.String("event", "", "expected hook event (pre, post, session, prompt, stop); optional, the stdin payload is authoritative")
	capName := fs.String("cap", "", "only run this capability (guard, format, enrich); empty runs all registered for the event")
	timeout := fs.Duration("timeout", 10*time.Second, "maximum time a hook may take before it is abandoned")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s run [--event=<name>] [--cap=<name>] [--timeout=<duration>]\n\n"+
			"Reads a Claude Code hook event as JSON on stdin and writes the response on stdout.\n"+
			"Not intended to be run by hand; %s init wires it up.\n\nFlags:\n", binName, binName)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 0 // fail open: a flag typo must not break the user's session
	}

	if code := run(os.Stdin, os.Stdout, *event, *capName, *timeout); code != 0 {
		return code
	}
	return 0
}

// run is the testable core of runCmd.
func run(stdin io.Reader, stdout io.Writer, wantEvent, wantCap string, timeout time.Duration) int {
	e, err := payload.Decode(stdin)
	if err != nil {
		debugf("run: %v", err)
		return 0
	}

	// --event is a readability aid in settings.json, not a routing input. If
	// it disagrees with the payload the config is miswired, which is worth
	// recording, but the payload still wins because it is ground truth.
	if wantEvent != "" {
		canon, ok := canonicalEvent(wantEvent)
		switch {
		case !ok:
			debugf("run: unrecognised --event=%q, ignoring", wantEvent)
		case canon != e.HookEventName:
			debugf("run: --event=%s but payload says %s; trusting the payload", wantEvent, e.HookEventName)
		}
	}
	// --cap narrows dispatch to one capability; otherwise every non-disabled
	// capability runs (the plugin's hooks/hooks.json form, which cannot carry
	// a per-capability switch). The enabled set lives in the toolkit's private
	// capcfg, so `manage disable` works for both registration paths.
	var caps []string
	disabled, _ := capcfg.Disabled()
	if wantCap != "" {
		if disabled[wantCap] {
			// Disabled via manage: no opinion, but still a valid JSON doc so
			// Claude Code does not show a spurious hook error.
			io.WriteString(stdout, "{}\n")
			return 0
		}
		caps = []string{wantCap}
	} else if len(disabled) > 0 {
		for _, r := range hooks.Register().Routes() {
			if !disabled[r.Name] {
				caps = append(caps, r.Name)
			}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	resp, err := hooks.Register().Dispatch(ctx, e, caps...)
	if err != nil {
		// A handler failed. Emit whatever the others decided rather than
		// discarding a legitimate deny because an unrelated hook broke.
		debugf("run: %s: %v", e.HookEventName, err)
	}

	// Buffer first: a half-written JSON document on stdout is worse than none,
	// because Claude Code would try to parse the fragment.
	var buf bytes.Buffer
	if err := resp.Write(&buf); err != nil {
		debugf("run: encode response: %v", err)
		return 0
	}
	if buf.Len() == 0 {
		// Claude Code shows a spurious "<hook> hook error" when a hook exits 0
		// with completely empty stdout (#17088). An empty JSON object is the
		// same "no opinion" without tripping that.
		if _, err := stdout.Write([]byte("{}\n")); err != nil {
			debugf("run: write stdout: %v", err)
		}
		return 0
	}
	if _, err := stdout.Write(buf.Bytes()); err != nil {
		debugf("run: write stdout: %v", err)
	}
	return 0
}
