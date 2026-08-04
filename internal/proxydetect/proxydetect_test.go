package proxydetect

import (
	"os"
	"testing"
)

// clearEnv wipes every key Detect inspects so a stray export from another
// test (or the developer's shell) cannot leak in. t.Setenv returns the var
// to its prior value on cleanup; we use Unsetenv for the explicit
// "never-set" baseline so we can distinguish "set to empty" from "never
// touched" later in the test.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range envKeys {
		if old, ok := os.LookupEnv(k); ok {
			old := old
			k := k
			t.Cleanup(func() { _ = os.Setenv(k, old) })
		}
		_ = os.Unsetenv(k)
	}
}

func TestDetect_NoneSet(t *testing.T) {
	clearEnv(t)
	url, ok := Detect()
	if ok || url != "" {
		t.Fatalf("Detect with no env = (%q, %v), want (\"\", false)", url, ok)
	}
}

func TestDetect_HTTPSOnly(t *testing.T) {
	clearEnv(t)
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:7890")
	url, ok := Detect()
	if !ok || url != "http://127.0.0.1:7890" {
		t.Fatalf("HTTPS_PROXY only = (%q, %v)", url, ok)
	}
}

func TestDetect_HTTPOnly(t *testing.T) {
	clearEnv(t)
	t.Setenv("HTTP_PROXY", "http://10.0.0.1:3128")
	url, ok := Detect()
	if !ok || url != "http://10.0.0.1:3128" {
		t.Fatalf("HTTP_PROXY only = (%q, %v)", url, ok)
	}
}

func TestDetect_ALLOnly(t *testing.T) {
	clearEnv(t)
	t.Setenv("ALL_PROXY", "socks5://192.168.1.1:1080")
	url, ok := Detect()
	if !ok || url != "socks5://192.168.1.1:1080" {
		t.Fatalf("ALL_PROXY only = (%q, %v)", url, ok)
	}
}

func TestDetect_HTTPSBeatsHTTP(t *testing.T) {
	clearEnv(t)
	t.Setenv("HTTPS_PROXY", "http://h.example:1")
	t.Setenv("HTTP_PROXY", "http://p.example:2")
	url, ok := Detect()
	if !ok || url != "http://h.example:1" {
		t.Fatalf("HTTPS > HTTP = (%q, %v)", url, ok)
	}
}

func TestDetect_HTTPBeatsALL(t *testing.T) {
	clearEnv(t)
	t.Setenv("HTTP_PROXY", "http://p.example:2")
	t.Setenv("ALL_PROXY", "socks5://a.example:3")
	url, ok := Detect()
	if !ok || url != "http://p.example:2" {
		t.Fatalf("HTTP > ALL = (%q, %v)", url, ok)
	}
}

func TestDetect_EmptyStringIsUnset(t *testing.T) {
	clearEnv(t)
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("ALL_PROXY", "")
	url, ok := Detect()
	if ok || url != "" {
		t.Fatalf("all-empty env = (%q, %v), want (\"\", false)", url, ok)
	}
}

func TestDetect_LowercaseIgnored(t *testing.T) {
	// Go's http.ProxyFromEnvironment only checks uppercase. We mirror that.
	clearEnv(t)
	t.Setenv("http_proxy", "http://lowercase.example:1")
	url, ok := Detect()
	if ok || url != "" {
		t.Fatalf("lowercase env should be ignored, got (%q, %v)", url, ok)
	}
}
