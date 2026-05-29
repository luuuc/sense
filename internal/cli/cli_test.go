package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/luuuc/sense/internal/search"
)

// newTestIO returns an IO with byte buffer sinks and Dir="." so a
// table test can inspect what a runner wrote without touching the
// real filesystem.
func newTestIO() (IO, *bytes.Buffer, *bytes.Buffer) {
	var stdout, stderr bytes.Buffer
	return IO{Stdout: &stdout, Stderr: &stderr, Dir: "."}, &stdout, &stderr
}

// Each subcommand must accept --help and exit 0 with the full help
// block on stderr. Pinning the key phrases (rather than the whole
// block) keeps the test stable against minor copy edits while still
// asserting the externally-visible shape.
func TestRunGraphHelp(t *testing.T) {
	for _, flag := range []string{"--help", "-h"} {
		cio, _, stderr := newTestIO()
		if code := RunGraph([]string{flag}, cio); code != ExitSuccess {
			t.Fatalf("%s: exit code = %d, want %d", flag, code, ExitSuccess)
		}
		got := stderr.String()
		for _, want := range []string{
			"usage: sense graph <symbol>",
			"--depth N",
			"--direction DIR",
			"--json",
			"Exit codes:",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("%s: help missing %q\ngot:\n%s", flag, want, got)
			}
		}
	}
}

func TestRunBlastHelp(t *testing.T) {
	for _, flag := range []string{"--help", "-h"} {
		cio, _, stderr := newTestIO()
		if code := RunBlast([]string{flag}, cio); code != ExitSuccess {
			t.Fatalf("%s: exit code = %d, want %d", flag, code, ExitSuccess)
		}
		got := stderr.String()
		for _, want := range []string{
			"usage: sense blast <symbol>",
			"sense blast --diff <ref>",
			"--max-hops N",
			"--include-tests",
			"Exit codes:",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("%s: help missing %q\ngot:\n%s", flag, want, got)
			}
		}
	}
}

func TestParseGraphArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    graphOptions
		wantErr bool
	}{
		{
			name: "default depth and direction",
			args: []string{"CheckoutService"},
			want: graphOptions{Symbol: "CheckoutService", Depth: 1, Direction: DirectionBoth},
		},
		{
			name: "callers direction",
			args: []string{"--direction", "callers", "User#email"},
			want: graphOptions{Symbol: "User#email", Depth: 1, Direction: DirectionCallers},
		},
		{
			name: "json flag",
			args: []string{"--json", "--depth", "2", "Foo"},
			want: graphOptions{Symbol: "Foo", Depth: 2, Direction: DirectionBoth, JSON: true},
		},
		{name: "missing symbol", args: nil, wantErr: true},
		{name: "bad direction", args: []string{"--direction", "upward", "Foo"}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			got, err := parseGraphArgs(tc.args, &stderr)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil; stderr=%q", stderr.String())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v; stderr=%q", err, stderr.String())
			}
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestRunSearchHelp(t *testing.T) {
	for _, flag := range []string{"--help", "-h"} {
		cio, _, stderr := newTestIO()
		if code := RunSearch([]string{flag}, cio); code != ExitSuccess {
			t.Fatalf("%s: exit code = %d, want %d", flag, code, ExitSuccess)
		}
		got := stderr.String()
		for _, want := range []string{
			"usage: sense search <query>",
			"--limit N",
			"--language LANG",
			"--min-score F",
			"--json",
			"Exit codes:",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("%s: help missing %q\ngot:\n%s", flag, want, got)
			}
		}
	}
}

func TestParseSearchArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    searchOptions
		wantErr bool
	}{
		{
			name: "query with defaults",
			args: []string{"payment error handling"},
			want: searchOptions{Query: "payment error handling", Limit: 10, Mode: search.ModeHybrid},
		},
		{
			name: "with flags",
			args: []string{"--limit", "5", "--language", "ruby", "--json", "auth flow"},
			want: searchOptions{Query: "auth flow", Limit: 5, Language: "ruby", JSON: true, Mode: search.ModeHybrid},
		},
		{
			name: "min-score flag",
			args: []string{"--min-score", "0.5", "test query"},
			want: searchOptions{Query: "test query", Limit: 10, MinScore: 0.5, Mode: search.ModeHybrid},
		},
		{name: "missing query", args: nil, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			got, err := parseSearchArgs(tc.args, &stderr)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil; stderr=%q", stderr.String())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v; stderr=%q", err, stderr.String())
			}
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestParseBlastArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    blastOptions
		wantErr bool
	}{
		{
			name: "symbol with defaults",
			args: []string{"User#email_verified?"},
			want: blastOptions{Symbol: "User#email_verified?", MaxHops: 3, MinConfidence: 0.7, IncludeTests: true},
		},
		{
			name: "diff form",
			args: []string{"--diff", "HEAD~1"},
			want: blastOptions{Diff: "HEAD~1", MaxHops: 3, MinConfidence: 0.7, IncludeTests: true},
		},
		{
			name: "symbol with flags",
			args: []string{"--max-hops", "5", "--json", "CheckoutService"},
			want: blastOptions{Symbol: "CheckoutService", MaxHops: 5, MinConfidence: 0.7, IncludeTests: true, JSON: true},
		},
		{name: "missing symbol and diff", args: nil, wantErr: true},
		{name: "both forms", args: []string{"--diff", "HEAD", "Foo"}, wantErr: true},
		{name: "bad confidence", args: []string{"--min-confidence", "1.5", "Foo"}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			got, err := parseBlastArgs(tc.args, &stderr)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil; stderr=%q", stderr.String())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v; stderr=%q", err, stderr.String())
			}
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestRunDoctorHelp(t *testing.T) {
	for _, flag := range []string{"--help", "-h"} {
		cio, _, stderr := newTestIO()
		if code := RunDoctor([]string{flag}, cio); code != ExitSuccess {
			t.Fatalf("%s: exit code = %d, want %d", flag, code, ExitSuccess)
		}
		got := stderr.String()
		for _, want := range []string{
			"usage: sense doctor",
			"--json",
			"Exit codes:",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("%s: help missing %q\ngot:\n%s", flag, want, got)
			}
		}
	}
}

func TestRunBenchmarkHelp(t *testing.T) {
	for _, flag := range []string{"--help", "-h"} {
		cio, _, stderr := newTestIO()
		if code := RunBenchmark([]string{flag}, cio); code != ExitSuccess {
			t.Fatalf("%s: exit code = %d, want %d", flag, code, ExitSuccess)
		}
		got := stderr.String()
		for _, want := range []string{
			"usage: sense benchmark",
			"--iterations",
			"--json",
			"Exit codes:",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("%s: help missing %q\ngot:\n%s", flag, want, got)
			}
		}
	}
}

func TestRunBenchmarkErrors(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStderr string
	}{
		{"missing index", nil, ExitIndexMissing, "no index found"},
		{"bad flag", []string{"--badflag"}, ExitGeneralError, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cio, _, stderr := newTestIO()
			if code := RunBenchmark(tt.args, cio); code != tt.wantCode {
				t.Fatalf("exit code = %d, want %d", code, tt.wantCode)
			}
			if tt.wantStderr != "" && !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr missing %q, got: %q", tt.wantStderr, stderr.String())
			}
		})
	}
}

func TestRunSetupHelp(t *testing.T) {
	for _, flag := range []string{"--help", "-h"} {
		cio, _, stderr := newTestIO()
		if code := RunSetup([]string{flag}, cio); code != ExitSuccess {
			t.Fatalf("%s: exit code = %d, want %d", flag, code, ExitSuccess)
		}
		got := stderr.String()
		for _, want := range []string{
			"usage: sense setup",
			"--tools",
			"Examples:",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("%s: help missing %q\ngot:\n%s", flag, want, got)
			}
		}
	}
}

func TestRunSetupErrors(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStderr string
	}{
		{"bad flag", []string{"--badflag"}, ExitGeneralError, ""},
		{"bad tools", []string{"--tools", "invalid-tool"}, ExitGeneralError, "sense setup:"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cio, _, stderr := newTestIO()
			if code := RunSetup(tt.args, cio); code != tt.wantCode {
				t.Fatalf("exit code = %d, want %d", code, tt.wantCode)
			}
			if tt.wantStderr != "" && !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr missing %q, got: %q", tt.wantStderr, stderr.String())
			}
		})
	}
}

func TestRunSetupAutoDetect(t *testing.T) {
	cio, stdout, _ := newTestIO()
	cio.Dir = t.TempDir()

	if code := RunSetup(nil, cio); code != ExitSuccess {
		t.Fatalf("RunSetup: exit code = %d, want %d", code, ExitSuccess)
	}
	// Auto-detect mode must print detection output and the setup summary.
	if !strings.Contains(stdout.String(), "Configuring") {
		t.Errorf("stdout missing setup summary, got:\n%s", stdout.String())
	}
}

func TestRunSetupExplicitTool(t *testing.T) {
	cio, stdout, _ := newTestIO()
	cio.Dir = t.TempDir()

	if code := RunSetup([]string{"--tools", "cursor"}, cio); code != ExitSuccess {
		t.Fatalf("RunSetup: exit code = %d, want %d", code, ExitSuccess)
	}
	out := stdout.String()
	if !strings.Contains(out, "Cursor") {
		t.Errorf("stdout missing Cursor configuration, got:\n%s", out)
	}
	// Detection output is suppressed when --tools is explicit.
	if strings.Contains(out, "Detected tools:") {
		t.Errorf("explicit --tools should skip detection output, got:\n%s", out)
	}
}

func TestRunSetupRunFails(t *testing.T) {
	cio, _, stderr := newTestIO()
	cio.Dir = "/nonexistent/sense-setup-test-path"

	if code := RunSetup([]string{"--tools", "claude-code"}, cio); code != ExitGeneralError {
		t.Fatalf("RunSetup: exit code = %d, want %d", code, ExitGeneralError)
	}
	if !strings.Contains(stderr.String(), "sense setup:") {
		t.Errorf("stderr missing prefix, got: %q", stderr.String())
	}
}

func TestDefaultIO(t *testing.T) {
	cio := DefaultIO()
	if cio.Stdout == nil {
		t.Error("DefaultIO.Stdout is nil")
	}
	if cio.Stderr == nil {
		t.Error("DefaultIO.Stderr is nil")
	}
	if cio.Dir == "" {
		t.Error("DefaultIO.Dir is empty")
	}
}
