package cmd

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/zealot00/claude-toolkit/internal/proxy"
)

// proxyCmd runs the optional local API proxy. It is intentionally NOT wired
// into `init`: nothing sets ANTHROPIC_BASE_URL for you, because a dead proxy
// would take Claude Code down with it. Start it explicitly and point the
// variable at it yourself.
func proxyCmd(args []string) int {
	fs := flag.NewFlagSet("proxy", flag.ContinueOnError)
	listen := fs.String("listen", proxy.DefaultListen, "address to listen on")
	upstream := fs.String("upstream", proxy.DefaultUpstream, "API base URL to forward to")
	retries := fs.Int("max-retries", 4, "429 retries before the response is returned")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s proxy [--listen=<addr>] [--upstream=<url>] [--max-retries=<n>]\n\n"+
			"Runs a local reverse proxy that retries HTTP 429 with exponential backoff.\n\n"+
			"NOT installed automatically: set ANTHROPIC_BASE_URL=http://127.0.0.1:8080 yourself.\n"+
			"If the proxy is not running, Claude Code is down -- that trade-off is yours.\n\nFlags:\n", binName)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fs.Usage()
		return 2
	}

	handler := proxy.NewHandler(proxy.Config{
		Listen:     *listen,
		Upstream:   *upstream,
		MaxRetries: *retries,
	})
	log.Printf("claude-toolkit proxy listening on %s -> %s (429 retries: %d)", *listen, *upstream, *retries)
	if err := http.ListenAndServe(*listen, handler); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
}
