// Package proxydetect inspects the OS environment for a network proxy URL.
//
// The check mirrors Go's net/http ProxyFromEnvironment: HTTPS_PROXY takes
// precedence, then HTTP_PROXY, then ALL_PROXY. Empty strings are treated as
// "unset" so a stray export "" does not silently trigger proxy behaviour.
//
// Why it exists: the toolkit's optional 429 retry proxy (internal/proxy)
// should only auto-spawn when the user has already accepted proxy-failure as
// a trade-off -- and the strongest signal we have for that is whether the
// user has set HTTPS_PROXY themselves. Without this signal, autoproxy would
// be a new failure surface that PLAN.md §9 explicitly rejected.
package proxydetect

import "os"

// Env keys in priority order. The first non-empty value wins.
var envKeys = []string{"HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY"}

// Detect returns the configured network proxy URL and whether one is set.
// Returns ("", false) when none of HTTPS_PROXY/HTTP_PROXY/ALL_PROXY is set
// or every one of them is the empty string.
func Detect() (string, bool) {
	for _, key := range envKeys {
		if v := os.Getenv(key); v != "" {
			return v, true
		}
	}
	return "", false
}
