package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luuuc/sense/internal/scan"
)

func indexedDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	// Write a Go file with enough structure for the index.
	goFile := filepath.Join(root, "main.go")
	if err := os.WriteFile(goFile, []byte("package main\n\nfunc UserService() {}\n\nfunc main() { UserService() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := scan.Run(context.Background(), scan.Options{
		Root:     root,
		Output:   io.Discard,
		Warnings: io.Discard,
	}); err != nil {
		t.Fatalf("scan: %v", err)
	}

	return root
}

func TestRunUnknownHook(t *testing.T) {
	var buf bytes.Buffer
	code := Run("nonexistent", ".", strings.NewReader("{}"), &buf)
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if buf.String() != "{}\n" {
		t.Errorf("output = %q, want {}", buf.String())
	}
}

func TestRunNoIndex(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	code := Run("session-start", dir, strings.NewReader("{}"), &buf)
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if buf.String() != "{}\n" {
		t.Errorf("output = %q, want {} (no index)", buf.String())
	}
}

func TestSessionStartReturnsStats(t *testing.T) {
	dir := indexedDir(t)
	var buf bytes.Buffer
	Run("session-start", dir, strings.NewReader("{}"), &buf)

	var resp messageResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp.Message == "" {
		t.Fatal("expected non-empty message")
	}
	if !strings.Contains(resp.Message, "Sense index:") {
		t.Errorf("message = %q, expected 'Sense index:' prefix", resp.Message)
	}
}

func TestPreCompactReturnsHubs(t *testing.T) {
	dir := indexedDir(t)
	var buf bytes.Buffer
	Run("pre-compact", dir, strings.NewReader("{}"), &buf)

	var resp messageResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if !strings.Contains(resp.Message, "Sense Index Summary") {
		t.Errorf("message = %q, expected summary", resp.Message)
	}
}

func TestSubagentStartReturnsGuidance(t *testing.T) {
	dir := indexedDir(t)
	var buf bytes.Buffer
	Run("subagent-start", dir, strings.NewReader("{}"), &buf)

	var resp hookResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if !strings.Contains(resp.AdditionalContext, "Sense index") {
		t.Errorf("context = %q, expected sense guidance", resp.AdditionalContext)
	}
}

func TestPreToolUseSymbolMatch(t *testing.T) {
	dir := indexedDir(t)
	input := `{"tool_name":"Grep","tool_input":{"pattern":"UserService"}}`
	var buf bytes.Buffer
	Run("pre-tool-use", dir, strings.NewReader(input), &buf)

	var resp hookResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp.AdditionalContext == "" {
		t.Fatal("expected additionalContext for known symbol")
	}
	if !strings.Contains(resp.AdditionalContext, "sense_graph") {
		t.Error("expected sense_graph suggestion")
	}
}

func TestPreToolUseRegexNoOp(t *testing.T) {
	dir := indexedDir(t)
	input := `{"tool_name":"Grep","tool_input":{"pattern":"func.*Service"}}`
	var buf bytes.Buffer
	Run("pre-tool-use", dir, strings.NewReader(input), &buf)

	if buf.String() != "{}\n" {
		t.Errorf("regex pattern should be no-op, got %q", buf.String())
	}
}

func TestPreToolUseNoMatch(t *testing.T) {
	dir := indexedDir(t)
	input := `{"tool_name":"Grep","tool_input":{"pattern":"NonexistentSymbol"}}`
	var buf bytes.Buffer
	Run("pre-tool-use", dir, strings.NewReader(input), &buf)

	if buf.String() != "{}\n" {
		t.Errorf("unknown symbol should be no-op, got %q", buf.String())
	}
}

func TestIsSymbolShaped(t *testing.T) {
	cases := []struct {
		pattern string
		want    bool
	}{
		{"UserService", true},
		{"user_service", true},
		{"User.find", true},
		{"User::Service", true},
		{"User#method", true},
		{"func.*Adapter", false},
		{"TODO|FIXME", false},
		{"(test)", false},
		{"[a-z]+", false},
		{"a", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isSymbolShaped(tc.pattern); got != tc.want {
			t.Errorf("isSymbolShaped(%q) = %v, want %v", tc.pattern, got, tc.want)
		}
	}
}
