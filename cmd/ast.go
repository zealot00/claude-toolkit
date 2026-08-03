package cmd

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zealot00/claude-toolkit/internal/astsum"
)

// astCmd prints a compressed structural summary of a Go or Python source file
// (package/types/functions signatures, no bodies) as JSON, so Claude can read
// a file's shape at a fraction of the token cost.
func astCmd(args []string) int {
	fs := flag.NewFlagSet("ast", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s ast <file.go|file.py>\n\n"+
			"Prints a compressed JSON summary of the file's structure: package,\n"+
			"types, function and method signatures, without bodies.\n", binName)
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fs.Usage()
		return 2
	}
	path := rest[0]

	var summary any
	var err error
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		summary, err = astsum.SummarizeGo(path)
	case ".py":
		summary, err = astsum.SummarizePy(path)
	default:
		fmt.Fprintf(os.Stderr, "error: %s is not a .go or .py file\n", path)
		return 2
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	out, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: encode summary: %v\n", err)
		return 1
	}
	fmt.Println(string(out))
	return 0
}
