package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"testing"
)

// runOut is the common shape: run a command with empty stdin and captured
// stdout/stderr, returning the exit code and the two buffers' contents.
func runOut(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, strings.NewReader(""), &out, &errb)
	return code, out.String(), errb.String()
}

func TestRunNoArgsPrintsHelpToStderr(t *testing.T) {
	code, out, errb := runOut(t)
	if code != 2 {
		t.Fatalf("no-args exit = %d, want 2", code)
	}
	if !strings.Contains(errb, "Usage: sense") {
		t.Errorf("help not on stderr: %q", errb)
	}
	if out != "" {
		t.Errorf("nothing should go to stdout, got %q", out)
	}
}

func TestRunVersion(t *testing.T) {
	for _, alias := range []string{"version", "--version", "-v"} {
		code, out, _ := runOut(t, alias)
		if code != 0 {
			t.Fatalf("%s exit = %d, want 0", alias, code)
		}
		if !strings.Contains(out, "sense ") || !strings.Contains(out, "schema v") {
			t.Errorf("%s output = %q", alias, out)
		}
	}
}

func TestRunHelp(t *testing.T) {
	for _, alias := range []string{"help", "--help", "-h"} {
		code, out, _ := runOut(t, alias)
		if code != 0 {
			t.Fatalf("%s exit = %d, want 0", alias, code)
		}
		if !strings.Contains(out, "Usage: sense") {
			t.Errorf("%s output = %q", alias, out)
		}
	}
}

func TestRunUnknownCommand(t *testing.T) {
	code, _, errb := runOut(t, "frobnicate")
	if code != 1 {
		t.Fatalf("unknown-command exit = %d, want 1", code)
	}
	if !strings.Contains(errb, `unknown command "frobnicate"`) {
		t.Errorf("stderr = %q", errb)
	}
}

func TestRunHookMissingSubcommand(t *testing.T) {
	code, _, errb := runOut(t, "hook")
	if code != 1 {
		t.Fatalf("hook (no sub) exit = %d, want 1", code)
	}
	if !strings.Contains(errb, "usage: sense hook") {
		t.Errorf("stderr = %q", errb)
	}
}

func TestRunHookDispatches(t *testing.T) {
	// session-start with empty stdin and no index in cwd: hook.Run fails open,
	// writes {} to stdout, and returns 0. We only need the dispatch line covered.
	t.Chdir(t.TempDir())
	var out bytes.Buffer
	code := run([]string{"hook", "session-start"}, strings.NewReader("{}"), &out, io.Discard)
	if code != 0 {
		t.Fatalf("hook dispatch exit = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "{}") {
		t.Errorf("hook stdout = %q", out.String())
	}
}

// TestRunQueryCommandsDispatch drives every query/config subcommand against an
// empty working directory and pins the exit code run returns for each — proving
// the dispatch arm not only executes but returns the documented code. setup
// --help short-circuits to success; the index readers report a missing index
// (3); doctor diagnoses the missing index as a general error (1).
func TestRunQueryCommandsDispatch(t *testing.T) {
	cases := []struct {
		args []string
		want int
	}{
		{[]string{"setup", "--help"}, 0},  // help: ExitSuccess
		{[]string{"search", "needle"}, 3}, // ExitIndexMissing
		{[]string{"graph", "Sym"}, 3},     // ExitIndexMissing
		{[]string{"blast", "Sym"}, 3},     // ExitIndexMissing
		{[]string{"dead"}, 3},             // ExitIndexMissing
		{[]string{"conventions"}, 3},      // ExitIndexMissing
		{[]string{"status"}, 3},           // ExitIndexMissing
		{[]string{"benchmark"}, 3},        // ExitIndexMissing
		{[]string{"doctor"}, 1},           // ExitGeneralError
	}
	for _, c := range cases {
		t.Run(c.args[0], func(t *testing.T) {
			t.Chdir(t.TempDir())
			if got := run(c.args, strings.NewReader(""), io.Discard, io.Discard); got != c.want {
				t.Errorf("run(%v) = %d, want %d", c.args, got, c.want)
			}
		})
	}
}

func TestRunMCPMissingIndex(t *testing.T) {
	// An empty dir has no index, so mcpserver.Run fails to build the server and
	// returns before ServeStdio — covering the error arm without blocking.
	code, _, errb := runOut(t, "mcp", "--dir", t.TempDir())
	if code != 1 {
		t.Fatalf("mcp missing-index exit = %d, want 1", code)
	}
	if !strings.Contains(errb, "sense mcp:") {
		t.Errorf("stderr = %q", errb)
	}
}

func TestRunMCPFlagParseError(t *testing.T) {
	code, _, _ := runOut(t, "mcp", "--nonexistent-flag")
	if code != 1 {
		t.Fatalf("mcp bad-flag exit = %d, want 1", code)
	}
}

func TestRunScanFlagParseError(t *testing.T) {
	if code := runScan([]string{"--nonexistent-flag"}, io.Discard); code != 1 {
		t.Fatalf("scan bad-flag exit = %d, want 1", code)
	}
}

func TestRunScanCPUProfileCreateError(t *testing.T) {
	// Parent directory does not exist, so os.Create fails.
	bad := filepath.Join(t.TempDir(), "missing", "cpu.prof")
	var errb bytes.Buffer
	if code := runScan([]string{"--cpuprofile", bad}, &errb); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "create cpuprofile") {
		t.Errorf("stderr = %q", errb.String())
	}
}

func TestRunScanCPUProfileAlreadyActive(t *testing.T) {
	// Start a CPU profile so runScan's StartCPUProfile errors ("already in use").
	if err := pprof.StartCPUProfile(io.Discard); err != nil {
		t.Fatalf("precondition StartCPUProfile: %v", err)
	}
	defer pprof.StopCPUProfile()

	good := filepath.Join(t.TempDir(), "cpu.prof")
	var errb bytes.Buffer
	if code := runScan([]string{"--cpuprofile", good}, &errb); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "start cpuprofile") {
		t.Errorf("stderr = %q", errb.String())
	}
}

func TestRunScanWatchInitialScanError(t *testing.T) {
	// --dir points at a regular file, so the watcher's initial scan can't create
	// <file>/.sense and watch.Run returns an error fast (no blocking watcher).
	f := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// watch.Run logs to os.Stderr directly; silence it for a clean test run.
	defer silenceStderr(t)()
	if code := runScan([]string{"--watch", "--dir", f}, io.Discard); code != 1 {
		t.Fatalf("watch initial-scan-error exit = %d, want 1", code)
	}
}

func TestRunScanSuccessWithProfiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"),
		[]byte("package a\n\nfunc Hello() string { return \"hi\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cpu := filepath.Join(t.TempDir(), "cpu.prof")
	mem := filepath.Join(t.TempDir(), "mem.prof")

	code := runScan([]string{"--quiet", "--dir", dir, "--cpuprofile", cpu, "--memprofile", mem}, io.Discard)
	if code != 0 {
		t.Fatalf("scan success exit = %d, want 0", code)
	}
	for _, p := range []string{cpu, mem, filepath.Join(dir, ".sense", "index.db")} {
		if fi, err := os.Stat(p); err != nil || fi.Size() == 0 {
			t.Errorf("expected non-empty %s (err=%v)", p, err)
		}
	}
}

func TestRunScanMemProfileCreateError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(t.TempDir(), "missing", "mem.prof")
	var errb bytes.Buffer
	if code := runScan([]string{"--quiet", "--dir", dir, "--memprofile", bad}, &errb); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "create memprofile") {
		t.Errorf("stderr = %q", errb.String())
	}
}

func TestMainExitsWithRunCode(t *testing.T) {
	oldArgs, oldExit := os.Args, osExit
	defer func() { os.Args, osExit = oldArgs, oldExit }()

	var got int
	osExit = func(code int) { got = code }
	os.Args = []string{"sense", "version"}
	defer silenceStdout(t)()

	main()
	if got != 0 {
		t.Fatalf("main passed exit code %d to osExit, want 0", got)
	}
}

// silenceStdout/silenceStderr redirect the real stream to /dev/null for the
// duration of a test that exercises code writing to os.Stdout/os.Stderr
// directly. The returned func restores the original. NOT parallel-safe: they
// swap a process global, so callers must not run under t.Parallel().
func silenceStdout(t *testing.T) func() {
	t.Helper()
	orig := os.Stdout
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = devnull
	return func() { os.Stdout = orig; _ = devnull.Close() }
}

func silenceStderr(t *testing.T) func() {
	t.Helper()
	orig := os.Stderr
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = devnull
	return func() { os.Stderr = orig; _ = devnull.Close() }
}
