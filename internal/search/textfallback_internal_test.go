package search

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseRgOutputBasic(t *testing.T) {
	output := "foo.go:10:func main() {}\nbar.go:42:CREATE TABLE users\n"
	results := parseRgOutput(output, 10)
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if results[0].File != "foo.go" || results[0].Line != 10 {
		t.Errorf("result[0] = %+v", results[0])
	}
	if results[1].File != "bar.go" || results[1].Line != 42 {
		t.Errorf("result[1] = %+v", results[1])
	}
	if results[1].Match != "CREATE TABLE users" {
		t.Errorf("match = %q, want %q", results[1].Match, "CREATE TABLE users")
	}
}

func TestParseRgOutputRespectsLimit(t *testing.T) {
	output := "a.go:1:one\nb.go:2:two\nc.go:3:three\n"
	results := parseRgOutput(output, 2)
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
}

func TestParseRgOutputSkipsMalformed(t *testing.T) {
	output := "no-colon\nfoo.go:notanum:text\nbar.go:1:valid\n"
	results := parseRgOutput(output, 10)
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].File != "bar.go" {
		t.Errorf("File = %q, want %q", results[0].File, "bar.go")
	}
}

func TestParseRgOutputWithColonsInMatch(t *testing.T) {
	output := "config.yaml:5:url: https://example.com:8080/path\n"
	results := parseRgOutput(output, 10)
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].File != "config.yaml" {
		t.Errorf("File = %q, want %q", results[0].File, "config.yaml")
	}
	if results[0].Line != 5 {
		t.Errorf("Line = %d, want 5", results[0].Line)
	}
	if results[0].Match != "url: https://example.com:8080/path" {
		t.Errorf("Match = %q, want %q", results[0].Match, "url: https://example.com:8080/path")
	}
}

func TestTextFallbackNotAvailable(t *testing.T) {
	tf := &TextFallback{}
	if tf.Available() {
		t.Error("expected not available")
	}
	results := tf.Search(context.Background(), "test", "/tmp", []string{"a.go"}, 10)
	if results != nil {
		t.Error("expected nil results when rg unavailable")
	}
}

func TestTextFallbackEmptyQuery(t *testing.T) {
	tf := &TextFallback{rgPath: "/usr/bin/true"}
	results := tf.Search(context.Background(), "", t.TempDir(), []string{"a.go"}, 10)
	if results != nil {
		t.Error("expected nil for empty query")
	}
}

func TestTextFallbackNoPaths(t *testing.T) {
	tf := &TextFallback{rgPath: "/usr/bin/true"}
	results := tf.Search(context.Background(), "test", t.TempDir(), nil, 10)
	if results != nil {
		t.Error("expected nil for no paths")
	}
}

func TestTextFallbackTimeout(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "rg")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 10\n"), 0755); err != nil {
		t.Fatal(err)
	}

	tf := &TextFallback{rgPath: script}
	start := time.Now()
	results := tf.Search(context.Background(), "test", dir, []string{"."}, 10)
	elapsed := time.Since(start)

	if results != nil {
		t.Error("expected nil from timed-out search")
	}
	if elapsed > 3*time.Second {
		t.Errorf("took %v, expected ~1s timeout", elapsed)
	}
}
