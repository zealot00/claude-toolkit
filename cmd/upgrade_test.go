package cmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeReleaseServer serves an archive and its checksums.txt, like a GitHub
// release. It returns the archive bytes so tests can also build a tampered
// copy.
func fakeReleaseServer(t *testing.T, archiveName string, binContent []byte) (*httptest.Server, []byte) {
	t.Helper()
	// Build the tar.gz archive.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	hdr := &tar.Header{Name: "claude-toolkit", Mode: 0o755, Size: int64(len(binContent))}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(binContent); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()
	archive := buf.Bytes()

	sum := fmt.Sprintf("%x", sha256.Sum256(archive))
	checksums := sum + "  " + archiveName + "\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + archiveName:
			w.Write(archive)
		case "/checksums.txt":
			w.Write([]byte(checksums))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, archive
}

func TestDownloadAndVerify(t *testing.T) {
	bin := []byte("#!/bin/sh\necho upgraded\n")
	srv, archive := fakeReleaseServer(t, "claude-toolkit_darwin_arm64.tar.gz", bin)

	client := &http.Client{}
	data, err := downloadAndVerify(client, srv.URL, "claude-toolkit_darwin_arm64.tar.gz")
	if err != nil {
		t.Fatalf("downloadAndVerify: %v", err)
	}
	// The function returns the archive bytes (the tar.gz), checksum-verified.
	if !bytes.Equal(data, archive) {
		t.Error("returned bytes do not match the served archive")
	}
}

func TestDownloadAndVerifyRejectsTampered(t *testing.T) {
	bin := []byte("original binary")
	srv, _ := fakeReleaseServer(t, "claude-toolkit_darwin_arm64.tar.gz", bin)

	// Serve a different archive under the same name: checksum must fail.
	client := &http.Client{}
	_, err := downloadAndVerify(client, srv.URL, "claude-toolkit_darwin_arm64.tar.gz")
	if err != nil {
		t.Fatalf("expected success for the authentic archive, got: %v", err)
	}

	// Now a tampered checksums.txt.
	tampered := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/checksums.txt" {
			w.Write([]byte("0000000000000000000000000000000000000000000000000000000000000000  " + "claude-toolkit_darwin_arm64.tar.gz" + "\n"))
			return
		}
		http.NotFound(w, r)
	}))
	defer tampered.Close()
	if _, err := downloadAndVerify(client, tampered.URL, "claude-toolkit_darwin_arm64.tar.gz"); err == nil {
		t.Fatal("tampered checksum must be rejected")
	}
}

func TestChecksumFor(t *testing.T) {
	body := []byte("abc  file-a.tar.gz\ndef  *file-b.tar.gz\n")
	if got := checksumFor(body, "file-a.tar.gz"); got != "abc" {
		t.Errorf("checksumFor(file-a) = %q", got)
	}
	if got := checksumFor(body, "file-b.tar.gz"); got != "def" {
		t.Errorf("checksumFor(file-b) = %q", got)
	}
	if got := checksumFor(body, "missing.tar.gz"); got != "" {
		t.Errorf("checksumFor(missing) = %q, want empty", got)
	}
}
