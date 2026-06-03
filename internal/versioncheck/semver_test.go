package versioncheck

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

func TestUpdateAlreadyLatest_NonDev(t *testing.T) {
	// The Update function checks version.Version == "0.0.0-dev" first,
	// which always returns 1 in test builds. TestUpdateDevBuild in
	// coverage_test.go already covers that. The non-dev paths require
	// setting version.Version at build time, so we test the component
	// functions directly.

	// Exercise the isNewer false path (already latest).
	if isNewer("1.0.0", "1.0.0") {
		t.Error("isNewer(1.0.0, 1.0.0) should be false")
	}
	if isNewer("0.9.0", "1.0.0") {
		t.Error("isNewer(0.9.0, 1.0.0) should be false")
	}
}

func TestFetchLatestTagBothFail(t *testing.T) {
	withTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "HEAD" {
			// Return 200 (not 302) for redirect method -> fails
			w.WriteHeader(http.StatusOK)
			return
		}
		// API call also fails
		w.WriteHeader(http.StatusInternalServerError)
	}))

	_, err := fetchLatestTag()
	if err == nil {
		t.Fatal("expected error when both redirect and API fail")
	}
}

func TestFetchLatestTagAPI_EmptyBody(t *testing.T) {
	withTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Empty body -> json unmarshal returns empty tag
		_, _ = w.Write([]byte("{}"))
	}))

	tag, err := fetchLatestTagAPI()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tag != "" {
		t.Errorf("tag = %q, want empty string for empty JSON", tag)
	}
}

func TestFetchLatestTag_RedirectFailsFallbackAPI(t *testing.T) {
	withTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "HEAD" {
			// Return 404 for redirect -> fails
			w.WriteHeader(http.StatusNotFound)
			return
		}
		// API succeeds
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			TagName string `json:"tag_name"`
		}{TagName: "v3.0.0"})
	}))

	tag, err := fetchLatestTag()
	if err != nil {
		t.Fatalf("fetchLatestTag: %v", err)
	}
	if tag != "3.0.0" {
		t.Errorf("tag = %q, want %q", tag, "3.0.0")
	}
}

func TestInGopathBin_EmptyExe(t *testing.T) {
	if inGopathBin("", "/home/user/go") {
		t.Error("empty exe should not be in GOPATH bin")
	}
}

func TestParseSemver_ShortVersions(t *testing.T) {
	tests := []struct {
		input string
		want  [3]int
	}{
		{"1", [3]int{1, 0, 0}},
		{"1.2", [3]int{1, 2, 0}},
		{"v2", [3]int{2, 0, 0}},
	}
	for _, tt := range tests {
		got := parseSemver(tt.input)
		if got != tt.want {
			t.Errorf("parseSemver(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestIsNewer_PatchLevel(t *testing.T) {
	tests := []struct {
		latest, current string
		want            bool
	}{
		{"1.0.1", "1.0.0", true},
		{"1.0.0", "1.0.1", false},
		{"2.0.0", "1.9.9", true},
		{"0.0.1", "0.0.0", true},
	}
	for _, tt := range tests {
		got := isNewer(tt.latest, tt.current)
		if got != tt.want {
			t.Errorf("isNewer(%q, %q) = %v, want %v", tt.latest, tt.current, got, tt.want)
		}
	}
}

func TestUpdateDevBuildOutput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Update(&stdout, &stderr)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if stdout.Len() > 0 {
		t.Errorf("stdout should be empty for dev build, got %q", stdout.String())
	}
	out := stderr.String()
	if !bytes.Contains([]byte(out), []byte("development build")) {
		t.Errorf("stderr = %q, expected development build message", out)
	}
}
