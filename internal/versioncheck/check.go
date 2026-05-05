package versioncheck

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	repo        = "luuuc/sense"
	httpTimeout = 10 * time.Second
)

// Overridden by tests. Tests that mutate these must not use t.Parallel().
var (
	baseURL    = "https://github.com"
	apiBaseURL = "https://api.github.com"
)

func fetchLatestTag() (string, error) {
	// Use the redirect from /releases/latest — not subject to API rate limits.
	if tag, err := fetchLatestTagRedirect(); err == nil && tag != "" {
		return tag, nil
	}

	return fetchLatestTagAPI()
}

func fetchLatestTagRedirect() (string, error) {
	u := fmt.Sprintf("%s/%s/releases/latest", baseURL, repo)
	client := &http.Client{
		Timeout: httpTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	req, err := http.NewRequest("HEAD", u, nil)
	if err != nil {
		return "", err
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		return "", fmt.Errorf("expected 302, got %d", resp.StatusCode)
	}

	loc := resp.Header.Get("Location")
	const marker = "/releases/tag/"
	idx := strings.LastIndex(loc, marker)
	if idx < 0 {
		return "", fmt.Errorf("no tag in redirect URL")
	}
	return strings.TrimPrefix(loc[idx+len(marker):], "v"), nil
}

func fetchLatestTagAPI() (string, error) {
	url := fmt.Sprintf("%s/repos/%s/releases/latest", apiBaseURL, repo)
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
		return "", fmt.Errorf("GitHub API: %s (rate limit may be exceeded — set GITHUB_TOKEN to authenticate)", resp.Status)
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
