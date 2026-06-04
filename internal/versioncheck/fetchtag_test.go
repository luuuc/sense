package versioncheck

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func withTestServer(t *testing.T, handler http.Handler) {
	t.Helper()
	ts := httptest.NewServer(handler)
	origBase := baseURL
	origAPI := apiBaseURL
	baseURL = ts.URL
	apiBaseURL = ts.URL
	t.Cleanup(func() {
		ts.Close()
		baseURL = origBase
		apiBaseURL = origAPI
	})
}

func TestFetchLatestTag_NewerVersion(t *testing.T) {
	withTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", baseURL+"/luuuc/sense/releases/tag/v0.9.0")
		w.WriteHeader(http.StatusFound)
	}))

	tag, err := fetchLatestTag()
	if err != nil {
		t.Fatalf("fetchLatestTag: %v", err)
	}
	if tag != "0.9.0" {
		t.Errorf("tag = %q, want %q", tag, "0.9.0")
	}
}

func TestFetchLatestTag_FallbackToAPI(t *testing.T) {
	withTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "HEAD" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			TagName string `json:"tag_name"`
		}{TagName: "v1.2.3"})
	}))

	tag, err := fetchLatestTag()
	if err != nil {
		t.Fatalf("fetchLatestTag: %v", err)
	}
	if tag != "1.2.3" {
		t.Errorf("tag = %q, want %q", tag, "1.2.3")
	}
}

func TestFetchLatestTagRedirect_Success(t *testing.T) {
	withTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", baseURL+"/luuuc/sense/releases/tag/v0.8.0")
		w.WriteHeader(http.StatusFound)
	}))

	tag, err := fetchLatestTagRedirect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tag != "0.8.0" {
		t.Errorf("tag = %q, want %q", tag, "0.8.0")
	}
}

func TestFetchLatestTagRedirect_Non302(t *testing.T) {
	withTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	_, err := fetchLatestTagRedirect()
	if err == nil {
		t.Fatal("expected error for non-302 response")
	}
}

func TestFetchLatestTagRedirect_NoTagInLocation(t *testing.T) {
	withTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "https://github.com/luuuc/sense/releases")
		w.WriteHeader(http.StatusFound)
	}))

	_, err := fetchLatestTagRedirect()
	if err == nil {
		t.Fatal("expected error for missing tag in redirect URL")
	}
}

func TestFetchLatestTagAPI_Success(t *testing.T) {
	withTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			TagName string `json:"tag_name"`
		}{TagName: "v2.0.0"})
	}))

	tag, err := fetchLatestTagAPI()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tag != "2.0.0" {
		t.Errorf("tag = %q, want %q", tag, "2.0.0")
	}
}

func TestFetchLatestTagAPI_ServerError(t *testing.T) {
	withTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))

	_, err := fetchLatestTagAPI()
	if err == nil {
		t.Fatal("expected error for 403 response")
	}
}

func TestFetchLatestTagAPI_MalformedJSON(t *testing.T) {
	withTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{{not json"))
	}))

	_, err := fetchLatestTagAPI()
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestFetchLatestTag_Unreachable(t *testing.T) {
	origBase := baseURL
	origAPI := apiBaseURL
	baseURL = "http://127.0.0.1:1" // port 1 — guaranteed to fail
	apiBaseURL = "http://127.0.0.1:1"
	defer func() {
		baseURL = origBase
		apiBaseURL = origAPI
	}()

	_, err := fetchLatestTag()
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
}
