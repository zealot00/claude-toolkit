package cmd

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// logPath is where run diagnostics land when CLAUDE_TOOLKIT_DEBUG is set.
func logPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "claude-toolkit.log"), nil
}

// logCmd tails the toolkit's debug log. With --follow it streams new lines;
// otherwise it prints the last --lines lines. --event filters to one hook
// event name.
func logCmd(args []string) int {
	fs := flag.NewFlagSet("log", flag.ContinueOnError)
	follow := fs.Bool("follow", false, "keep streaming new lines")
	lines := fs.Int("lines", 50, "lines to print from the tail")
	event := fs.String("event", "", "only show lines mentioning this event name (pre, post, session, ...)")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s log [--follow] [--lines=<n>] [--event=<name>]\n\n"+
			"Reads the debug log written when CLAUDE_TOOLKIT_DEBUG is set.\n\nFlags:\n", binName)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	path, err := logPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		fmt.Printf("no log yet at %s\n", path)
		fmt.Println("Run Claude Code with CLAUDE_TOOLKIT_DEBUG=1 set, then check again.")
		return 0
	}

	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer f.Close()

	if *follow {
		return tailFollow(f, *event)
	}
	return tailLinesOf(os.Stdout, f, *lines, *event)
}

func tailLinesOf(w io.Writer, f *os.File, n int, event string) int {
	var all []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if event != "" && !strings.Contains(line, event) {
			continue
		}
		all = append(all, line)
	}
	if len(all) > n {
		all = all[len(all)-n:]
	}
	for _, l := range all {
		fmt.Fprintln(w, l)
	}
	return 0
}

// tailFollow streams appended lines, polling the file every 500ms. The log
// file is append-only via debugf, so a simple size cursor is enough.
func tailFollow(f *os.File, event string) int {
	cursor := int64(0)
	if st, err := f.Stat(); err == nil {
		cursor = st.Size()
		if _, err := f.Seek(cursor, io.SeekStart); err != nil {
			cursor = 0
		}
	}
	for {
		st, err := f.Stat()
		if err != nil {
			return 1
		}
		if st.Size() > cursor {
			buf := make([]byte, st.Size()-cursor)
			if _, err := f.ReadAt(buf, cursor); err == nil {
				for _, line := range strings.Split(string(buf), "\n") {
					if line == "" {
						continue
					}
					if event == "" || strings.Contains(line, event) {
						fmt.Println(line)
					}
				}
			}
			cursor = st.Size()
		}
		time.Sleep(500 * time.Millisecond)
	}
}
