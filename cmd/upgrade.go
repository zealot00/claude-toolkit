package cmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/zealot00/claude-toolkit/internal/proxydetect"
	"github.com/zealot00/claude-toolkit/internal/upgrader"
	"github.com/zealot00/claude-toolkit/pkg/dir"
)

// releaseInfo is the subset of the GitHub Releases API response we need.
type releaseInfo struct {
	TagName string `json:"tag_name"`
	URL     string `json:"html_url"`
}

// upgradeCmd checks for a newer release and, without --check-only, downloads
// and atomically replaces the running binary. Network failures are never
// fatal for the rest of the session.
//
// The replacement is "smooth": the running binary is backed up to
// <self>.bak before any write, the new binary is verified by running
// `doctor` against it (unless --skip-doctor), and any failure rolls the
// binary back from .bak. Successful upgrades run the schema-migration
// registry and prompt the user to refresh the plugin via `init --force`.
func upgradeCmd(args []string) int {
	fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	checkOnly := fs.Bool("check-only", false, "just report whether a newer version exists")
	skipDoctor := fs.Bool("skip-doctor", false, "skip the post-upgrade self-test (faster; less safe)")
	cleanup := fs.Bool("cleanup", false, "remove the leftover backup file and exit")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s upgrade [--check-only] [--skip-doctor] [--cleanup]\n\n"+
			"Checks GitHub Releases for a newer version and replaces this binary.\n"+
			"The replacement is smooth: backs up first, verifies the new binary\n"+
			"with `doctor`, and rolls back on failure.\n\nFlags:\n", binName)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	// Resolve the current binary up front: every branch below needs it,
	// and a missing os.Executable here means we cannot operate.
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: locate current binary: %v\n", err)
		return 1
	}

	if *cleanup {
		if err := upgrader.Cleanup(self); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Println("Removed leftover backup file.")
		return 0
	}

	const repo = "zealot00/claude-toolkit"
	latest, err := fetchLatestRelease(repo)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	current := strings.TrimPrefix(Version, "v")
	want := strings.TrimPrefix(latest.TagName, "v")
	cmp := compareVersions(current, want)

	switch {
	case current == "dev" || current == "none":
		fmt.Printf("current: %s (dev build, cannot compare)\n", Version)
		fmt.Printf("latest : %s (%s)\n", latest.TagName, latest.URL)
		return 0
	case cmp >= 0:
		fmt.Printf("Already up to date (current %s, latest %s).\n", current, latest.TagName)
		return 0
	}

	fmt.Printf("A newer version is available: %s (you have %s)\n", latest.TagName, current)
	if *checkOnly {
		return 0
	}

	// Surface the network proxy in the log so a slow download is not a
	// mystery. The download itself uses Go's ProxyFromEnvironment, so this
	// is purely observability.
	if netProxy, ok := proxydetect.Detect(); ok {
		fmt.Printf("downloading via network proxy %s\n", netProxy)
	}

	// Backup BEFORE any write so a torn upgrade cannot leave the user
	// without a working binary.
	if _, err := upgrader.Backup(self); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Println("Backed up current binary.")

	if err := doUpgrade(repo, latest.TagName); err != nil {
		// Download / extract / replace failed. Try to put the old binary
		// back so the user is not stranded with a half-installed tool.
		_ = upgrader.Rollback(self)
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		fmt.Println("Fall back to the official installer:")
		fmt.Printf("  curl -fsSL https://raw.githubusercontent.com/%s/main/scripts/install.sh | bash\n", repo)
		return 1
	}

	// Verify the new binary actually runs and its self-test passes. The
	// doctor command is run as a fresh process against the on-disk path
	// (not the current one) so a wedged hook cannot deadlock the upgrade.
	if !*skipDoctor {
		doctor := exec.Command(self, "doctor")
		doctor.Stdout = os.Stdout
		doctor.Stderr = os.Stderr
		if err := doctor.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "doctor failed after upgrade: %v -- rolling back\n", err)
			if rerr := upgrader.Rollback(self); rerr != nil {
				fmt.Fprintf(os.Stderr, "rollback failed: %v\n", rerr)
			}
			fmt.Println("Fall back to the official installer:")
			fmt.Printf("  curl -fsSL https://raw.githubusercontent.com/%s/main/scripts/install.sh | bash\n", repo)
			return 1
		}
	}

	// Run any schema migrations the upgrade brought with us. Failures are
	// surfaced but do not roll back the binary: the upgrade itself is
	// sound, and a migration bug should not strand the user on the old
	// binary.
	if home, err := dir.Root(); err == nil {
		ran, merr := upgrader.RunMigrations(Version, home)
		if merr != nil {
			fmt.Fprintf(os.Stderr, "warning: migration step failed: %v\n", merr)
		}
		for _, r := range ran {
			fmt.Printf("ran migration: %s\n", r)
		}
	}

	fmt.Println("Upgraded successfully. Restart Claude Code for the new hooks to load.")
	fmt.Println("Run `claude-toolkit init --force` to refresh plugin and settings.json.")
	return 0
}

// doUpgrade downloads the release archive for the current platform, verifies
// its SHA-256 against the published checksums.txt, extracts the binary, and
// atomically replaces the running executable. On Windows the running exe
// cannot be overwritten, so it reports instructions instead of failing hard.
func doUpgrade(repo, tag string) error {
	platform := runtime.GOOS + "_" + runtime.GOARCH
	archive := "claude-toolkit_" + platform + ".tar.gz"
	baseURL := "https://github.com/" + repo + "/releases/download/" + tag

	client := &http.Client{Timeout: 60 * time.Second}

	data, err := downloadAndVerify(client, baseURL, archive)
	if err != nil {
		return err
	}

	// Extract the single binary from the tar.gz.
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var binData []byte
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read archive: %w", err)
		}
		if hdr.Typeflag == tar.TypeReg && filepath.Base(hdr.Name) == "claude-toolkit" {
			binData, err = io.ReadAll(tr)
			if err != nil {
				return fmt.Errorf("extract binary: %w", err)
			}
			break
		}
	}
	if len(binData) == 0 {
		return fmt.Errorf("archive %s contains no claude-toolkit binary", archive)
	}

	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate current binary: %w", err)
	}
	if runtime.GOOS == "windows" {
		return fmt.Errorf("cannot replace a running .exe on Windows; run the official installer instead")
	}
	if err := os.WriteFile(self+".new", binData, 0o755); err != nil {
		return fmt.Errorf("write staging binary: %w", err)
	}
	if err := os.Rename(self+".new", self); err != nil {
		os.Remove(self + ".new")
		return fmt.Errorf("replace binary: %w", err)
	}
	return nil
}

// downloadAndVerify fetches the archive and checksums.txt and returns the
// archive bytes after confirming its SHA-256 matches the published checksum.
func downloadAndVerify(client *http.Client, baseURL, archive string) ([]byte, error) {
	archiveURL := baseURL + "/" + archive
	resp, err := client.Get(archiveURL)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", archive, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: %s", archive, resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", archive, err)
	}

	sumsResp, err := client.Get(baseURL + "/checksums.txt")
	if err != nil {
		return nil, fmt.Errorf("download checksums.txt: %w", err)
	}
	defer sumsResp.Body.Close()
	if sumsResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download checksums.txt: %s", sumsResp.Status)
	}
	sums, err := io.ReadAll(sumsResp.Body)
	if err != nil {
		return nil, fmt.Errorf("read checksums.txt: %w", err)
	}

	expected := checksumFor(sums, archive)
	if expected == "" {
		return nil, fmt.Errorf("no checksum published for %s; refusing to install an unverified binary", archive)
	}
	actual := fmt.Sprintf("%x", sha256.Sum256(data))
	if actual != expected {
		return nil, fmt.Errorf("checksum mismatch for %s\n  expected %s\n  actual   %s", archive, expected, actual)
	}
	return data, nil
}

// checksumFor extracts the sha256 for name from a checksums.txt body.
func checksumFor(sums []byte, name string) string {
	for line := range strings.SplitSeq(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && (fields[1] == name || fields[1] == "*"+name) {
			return fields[0]
		}
	}
	return ""
}

// fetchLatestRelease queries the GitHub Releases API for the latest tag.
func fetchLatestRelease(repo string) (releaseInfo, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	url := "https://api.github.com/repos/" + repo + "/releases/latest"
	resp, err := client.Get(url)
	if err != nil {
		return releaseInfo{}, fmt.Errorf("reach GitHub Releases: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return releaseInfo{}, fmt.Errorf("GitHub Releases returned %s", resp.Status)
	}
	var r releaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return releaseInfo{}, fmt.Errorf("decode release info: %w", err)
	}
	if r.TagName == "" {
		return releaseInfo{}, fmt.Errorf("release has no tag_name")
	}
	return r, nil
}

// compareVersions compares dotted numeric versions: 0 if equal, 1 if a>b, -1
// if a<b. Non-numeric segments sort before numeric ones.
func compareVersions(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		av, bv := 0, 0
		if i < len(as) {
			if n, err := strconv.Atoi(as[i]); err == nil {
				av = n
			} else {
				av = -1 // non-numeric sorts low
			}
		}
		if i < len(bs) {
			if n, err := strconv.Atoi(bs[i]); err == nil {
				bv = n
			} else {
				bv = -1
			}
		}
		if av != bv {
			if av > bv {
				return 1
			}
			return -1
		}
	}
	return 0
}
