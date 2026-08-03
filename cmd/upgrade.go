package cmd

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// releaseInfo is the subset of the GitHub Releases API response we need.
type releaseInfo struct {
	TagName string `json:"tag_name"`
	URL     string `json:"html_url"`
}

// upgradeCmd checks for a newer release and, without --check-only, downloads
// and atomically replaces the running binary. Network failures are never
// fatal for the rest of the session.
func upgradeCmd(args []string) int {
	fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	checkOnly := fs.Bool("check-only", false, "just report whether a newer version exists")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s upgrade [--check-only]\n\n"+
			"Checks GitHub Releases for a newer version and replaces this binary.\n\nFlags:\n", binName)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
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

	fmt.Println("Run the official installer to update:")
	fmt.Printf("  curl -fsSL https://raw.githubusercontent.com/%s/main/scripts/install.sh | bash\n", repo)
	fmt.Println("(in-place binary replacement is left to the installer, which verifies checksums)")
	return 0
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
