package versioncheck

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/luuuc/sense/internal/version"
)

const (
	repo         = "luuuc/sense"
	checkInterval = 24 * time.Hour
	httpTimeout   = 2 * time.Second
)

type state struct {
	LastCheck     time.Time `json:"last_check"`
	LatestVersion string    `json:"latest_version"`
}

func stateFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".sense", "version-check")
}

func readState() (state, error) {
	var s state
	path := stateFile()
	if path == "" {
		return s, fmt.Errorf("no home dir")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return s, err
	}
	err = json.Unmarshal(data, &s)
	return s, err
}

func writeState(s state) {
	path := stateFile()
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	data, err := json.Marshal(s)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}

// CheckAndNotify performs a non-blocking version check. If a newer
// release exists and the last check was more than 24 hours ago, it
// prints a one-line notice to stderr. Never delays the caller — the
// HTTP call has a 2s timeout and failures are silently ignored.
func CheckAndNotify(stderr io.Writer) {
	if version.Version == "0.0.0-dev" {
		return
	}

	s, err := readState()
	if err != nil {
		// First run after install — seed the cache and skip the network
		// call. The user just installed the latest version so there is
		// nothing to report, and reaching out to api.github.com on first
		// launch triggers a macOS firewall prompt.
		writeState(state{LastCheck: time.Now(), LatestVersion: version.Version})
		return
	}

	if time.Since(s.LastCheck) < checkInterval {
		if s.LatestVersion != "" && isNewer(s.LatestVersion, version.Version) {
			printNotice(stderr, s.LatestVersion)
		}
		return
	}

	// Fire-and-forget: update the cache in a goroutine so we never block.
	// The notice prints on the next invocation from cached state, avoiding
	// interleaved stderr output mid-command.
	go func() {
		latest, err := fetchLatestTag()
		if err != nil {
			return
		}
		writeState(state{
			LastCheck:     time.Now(),
			LatestVersion: latest,
		})
	}()
}

func fetchLatestTag() (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	client := &http.Client{Timeout: httpTimeout}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "token "+token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("GitHub API: %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return "", err
	}

	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &release); err != nil {
		return "", err
	}

	return strings.TrimPrefix(release.TagName, "v"), nil
}

// isNewer returns true if latest is a greater semver than current.
// Only compares dotted numeric segments; pre-release suffixes make
// the version "older" (e.g., 0.0.0-dev < 0.1.0).
func isNewer(latest, current string) bool {
	lp := parseSemver(latest)
	cp := parseSemver(current)
	for i := 0; i < 3; i++ {
		if lp[i] != cp[i] {
			return lp[i] > cp[i]
		}
	}
	return false
}

func parseSemver(v string) [3]int {
	v = strings.TrimPrefix(v, "v")
	// Strip pre-release suffix (e.g., "0.0.0-dev" → "0.0.0")
	if idx := strings.IndexByte(v, '-'); idx >= 0 {
		v = v[:idx]
	}
	var parts [3]int
	for i, s := range strings.SplitN(v, ".", 3) {
		n := 0
		for _, c := range s {
			if c >= '0' && c <= '9' {
				n = n*10 + int(c-'0')
			}
		}
		parts[i] = n
	}
	return parts
}

func printNotice(w io.Writer, latest string) {
	_, _ = fmt.Fprintf(w, "A new version of sense is available (v%s → v%s). Run: curl -fsSL https://raw.githubusercontent.com/%s/main/install.sh | sh\n",
		version.Version, latest, repo)
}
