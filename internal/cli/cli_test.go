package cli

import (
	"bytes"
	"strings"
	"testing"
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
