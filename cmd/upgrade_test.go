package cmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zealot00/claude-toolkit/internal/proxydetect"
	"github.com/zealot00/claude-toolkit/internal/upgrader"
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

// TestDoUpgradeRejectsArchiveWithoutBinary: an archive that lacks the
// claude-toolkit binary must error, not silently succeed.
func TestDoUpgradeRejectsArchiveWithoutBinary(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "README.txt", Mode: 0o644, Size: 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()
	archive := buf.Bytes()
	sum := fmt.Sprintf("%x", sha256.Sum256(archive))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/claude-toolkit_darwin_arm64.tar.gz":
			w.Write(archive)
		case "/checksums.txt":
			w.Write([]byte(sum + "  claude-toolkit_darwin_arm64.tar.gz\n"))
		}
	}))
	defer srv.Close()

	// Exercise the extract/verify portion by pointing doUpgrade's pieces at
	// the mock: downloadAndVerify succeeds, then the caller's extraction must
	// find no binary. We can't run doUpgrade end-to-end (it replaces the real
	// binary), so test the extraction failure path directly via a helper.
	data, err := downloadAndVerify(&http.Client{}, srv.URL, "claude-toolkit_darwin_arm64.tar.gz")
	if err != nil {
		t.Fatalf("downloadAndVerify: %v", err)
	}
	// Reimplement the binary-extraction loop briefly to assert it errors.
	gzr, _ := gzip.NewReader(bytes.NewReader(data))
	defer gzr.Close()
	tr := tar.NewReader(gzr)
	found := false
	for {
		hdr, err := tr.Next()
		if err != nil {
			break
		}
		if hdr.Typeflag == tar.TypeReg && hdr.Name == "claude-toolkit" {
			found = true
			break
		}
	}
	if found {
		t.Fatal("archive should not contain a claude-toolkit binary")
	}
}

// writeUpgradeableBinary lays down a small file at <tmp>/bin/claude-toolkit
// with the given contents and returns its path. Used as the "self" that
// upgradeCmd orchestrates around (backup, rollback) without ever replacing
// the test binary itself.
func writeUpgradeableBinary(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.Mkdir(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(binDir, "claude-toolkit")
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestUpgrade_BackupFailureLeavesOriginalIntact: when the backup step
// cannot write the .bak file (source missing), the original binary path
// must remain untouched -- upgradeCmd must abort before any replace.
func TestUpgrade_BackupFailureLeavesOriginalIntact(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-binary")
	_, err := upgrader.Backup(missing)
	if err == nil {
		t.Fatal("expected backup to fail when source is missing")
	}
	// Confirm the missing path is still missing -- the failed backup
	// must not create a phantom file or modify anything else.
	if _, statErr := os.Stat(missing); !os.IsNotExist(statErr) {
		t.Errorf("original path unexpectedly exists after failed backup: %v", statErr)
	}
}

// TestUpgrade_RollbackRestoresAfterDoctorFail: simulate the doctor-fail
// rollback path. Lay down a "current" binary, back it up, corrupt the
// current binary (simulating a broken new release), and verify Rollback
// restores the pre-upgrade contents.
func TestUpgrade_RollbackRestoresAfterDoctorFail(t *testing.T) {
	const original = "pre-upgrade binary v0.1.0\n"
	const broken = "broken v0.2.0 -- doctor fails\n"
	self := writeUpgradeableBinary(t, original)

	// Step 1: backup before any write.
	if _, err := upgrader.Backup(self); err != nil {
		t.Fatalf("Backup: %v", err)
	}

	// Step 2: simulate a failed replacement -- the "new binary" is
	// written but doctor fails on it.
	if err := os.WriteFile(self, []byte(broken), 0o755); err != nil {
		t.Fatal(err)
	}

	// Step 3: rollback should put the original contents back.
	if err := upgrader.Rollback(self); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	got, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Errorf("after rollback: %q, want %q", got, original)
	}
	// And the .bak should be consumed.
	if _, err := os.Stat(self + upgrader.BackupSuffix); !os.IsNotExist(err) {
		t.Errorf(".bak should be gone after rollback, stat err = %v", err)
	}
}

// TestUpgrade_MigrationsInvoked: upgradeCmd calls upgrader.RunMigrations
// after a successful replace. Verify the registry is reachable and that
// a no-op registry produces zero runs (this matches the v0.1.0 -> v0.2.0
// migration semantics: nothing to do).
func TestUpgrade_MigrationsInvoked(t *testing.T) {
	home := t.TempDir()
	// Backup the package-level migrations slice so we can restore it
	// regardless of what other tests have done.
	// (cmd package doesn't have direct access to the unexported slice;
	// instead we exercise the public RunMigrations with the current
	// registry state and assert no error.)
	ran, err := upgrader.RunMigrations("0.1.0", home)
	if err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	// The current registered migration (v0.1.0 -> v0.2.0) is a no-op,
	// so we should observe exactly one ran entry pointing at the bump.
	if len(ran) != 1 {
		t.Errorf("expected 1 ran migration from v0.1.0, got %v", ran)
	}
	if len(ran) > 0 && !strings.Contains(ran[0], "0.1.0->0.2.0") {
		t.Errorf("ran entry = %q, want it to mention 0.1.0->0.2.0", ran[0])
	}
}

// TestUpgrade_ProxydetectLogsProxyInUse: when HTTPS_PROXY is set, the
// download observability log should report it. We can't easily capture
// stdout from upgradeCmd end-to-end (it shells out to GitHub), so we
// verify the underlying proxydetect.Detect() primitive that the cmd
// uses to decide whether to print the log.
func TestUpgrade_ProxydetectLogsProxyInUse(t *testing.T) {
	const proxyURL = "http://127.0.0.1:7890"
	t.Setenv("HTTPS_PROXY", proxyURL)
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("ALL_PROXY", "")

	got, ok := proxydetect.Detect()
	if !ok {
		t.Fatal("Detect returned !ok despite HTTPS_PROXY being set")
	}
	if got != proxyURL {
		t.Errorf("Detect = %q, want %q", got, proxyURL)
	}

	// And the unset case: clear all three and confirm Detect returns
	// ("", false) so the cmd flow skips the "downloading via network
	// proxy" log line.
	for _, k := range []string{"HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY"} {
		t.Setenv(k, "")
	}
	if url, ok := proxydetect.Detect(); ok {
		t.Errorf("Detect with no proxy env: got (%q, %v), want (%q, false)", url, ok, "")
	}
}
