// Package proxy implements the optional local API proxy. It is deliberately a
// separate, explicitly-started module: nothing injects ANTHROPIC_BASE_URL for
// the user, because a dead proxy would take Claude Code down with it. Users
// who want 429 auto-retry start `claude-toolkit proxy` and point
// ANTHROPIC_BASE_URL at it themselves.
package proxy

import (
	"bytes"
	"io"
	"math"
	"math/rand"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

// Config controls the proxy listener and upstream.
type Config struct {
	// Listen is the bind address, e.g. "127.0.0.1:8080".
	Listen string
	// Upstream is the API base URL, e.g. "https://api.anthropic.com".
	Upstream string
	// MaxRetries caps 429 auto-retries before the response is returned.
	MaxRetries int
}

// NewHandler returns the reverse proxy with 429 retry handling.
func NewHandler(cfg Config) http.Handler {
	target, err := url.Parse(cfg.Upstream)
	if err != nil {
		// Config is validated in proxyCmd; reaching here is a programming error.
		panic("proxy: invalid upstream " + cfg.Upstream)
	}
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = pr.In.Host // preserve the original Host header
		},
		Transport: &RetryTransport{Base: http.DefaultTransport, MaxRetries: cfg.MaxRetries},
	}
	return rp
}

// RetryTransport re-issues a request that came back 429, with exponential
// backoff plus jitter. The body is buffered once so it can be replayed.
type RetryTransport struct {
	Base       http.RoundTripper
	MaxRetries int
}

// backoffBase is the initial 429 retry delay.
const backoffBase = 200 * time.Millisecond

func (t *RetryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.Base == nil {
		t.Base = http.DefaultTransport
	}

	// Buffer the body once so every attempt can replay it.
	var body []byte
	if req.Body != nil {
		var err error
		body, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.ContentLength = int64(len(body))
	}

	for attempt := 1; ; attempt++ {
		out := req.Clone(req.Context())
		if body != nil {
			out.Body = io.NopCloser(bytes.NewReader(body))
			out.ContentLength = int64(len(body))
		}

		resp, err := t.Base.RoundTrip(out)
		if err != nil {
			return resp, err
		}
		if resp.StatusCode != http.StatusTooManyRequests || attempt > t.MaxRetries {
			return resp, nil
		}

		// Drain and close so the connection can be reused, then back off.
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()

		backoff := time.Duration(float64(backoffBase) * math.Pow(2, float64(attempt-1)))
		jitter := time.Duration(rand.Int63n(int64(backoff / 2)))
		select {
		case <-time.After(backoff + jitter):
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	}
}

// Compile-time check that RetryTransport implements http.RoundTripper.
var _ http.RoundTripper = (*RetryTransport)(nil)

// DefaultListen and DefaultUpstream are the proxy defaults.
const (
	DefaultListen   = "127.0.0.1:8080"
	DefaultUpstream = "https://api.anthropic.com"
)
