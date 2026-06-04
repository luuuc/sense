package versioncheck

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/luuuc/sense/internal/version"
)

func TestIsGoInstall(t *testing.T) {
	// On macOS /var is a symlink to /private/var; resolve it up-front
	// so that filepath.EvalSymlinks inside isGoInstall sees consistent paths.
	t.Run("binary in gopath bin", func(t *testing.T) {
		tmp := t.TempDir()
		tmp, _ = filepath.EvalSymlinks(tmp)
		gopath := filepath.Join(tmp, "go")
		binDir := filepath.Join(gopath, "bin")
		if err := os.MkdirAll(binDir, 0755); err != nil {
			t.Fatal(err)
		}
		exePath := filepath.Join(binDir, "sense")
		if err := os.WriteFile(exePath, []byte("fake"), 0755); err != nil {
			t.Fatal(err)
		}

		orig := osExecutable
		osExecutable = func() (string, error) { return exePath, nil }
		t.Cleanup(func() { osExecutable = orig })
		t.Setenv("GOPATH", gopath)

		if !isGoInstall() {
			t.Error("expected true for binary in GOPATH/bin")
		}
	})

	t.Run("binary outside gopath", func(t *testing.T) {
		tmp := t.TempDir()
		tmp, _ = filepath.EvalSymlinks(tmp)
		gopath := filepath.Join(tmp, "go")
		usrBin := filepath.Join(tmp, "usr", "local", "bin")
		if err := os.MkdirAll(usrBin, 0755); err != nil {
			t.Fatal(err)
		}
		exePath := filepath.Join(usrBin, "sense")
		if err := os.WriteFile(exePath, []byte("fake"), 0755); err != nil {
			t.Fatal(err)
		}

		orig := osExecutable
		osExecutable = func() (string, error) { return exePath, nil }
		t.Cleanup(func() { osExecutable = orig })
		t.Setenv("GOPATH", gopath)

		if isGoInstall() {
			t.Error("expected false for binary outside GOPATH")
		}
	})

	t.Run("symlink into gopath bin", func(t *testing.T) {
		tmp := t.TempDir()
		tmp, _ = filepath.EvalSymlinks(tmp)
		gopath := filepath.Join(tmp, "go")
		binDir := filepath.Join(gopath, "bin")
		otherDir := filepath.Join(tmp, "other")
		if err := os.MkdirAll(binDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(otherDir, 0755); err != nil {
			t.Fatal(err)
		}
		realPath := filepath.Join(binDir, "sense")
		if err := os.WriteFile(realPath, []byte("fake"), 0755); err != nil {
			t.Fatal(err)
		}
		linkPath := filepath.Join(otherDir, "sense")
		if err := os.Symlink(realPath, linkPath); err != nil {
			t.Fatal(err)
		}

		orig := osExecutable
		osExecutable = func() (string, error) { return linkPath, nil }
		t.Cleanup(func() { osExecutable = orig })
		t.Setenv("GOPATH", gopath)

		if !isGoInstall() {
			t.Error("expected true for symlink resolving into GOPATH/bin")
		}
	})

	t.Run("symlink outside gopath", func(t *testing.T) {
		tmp := t.TempDir()
		tmp, _ = filepath.EvalSymlinks(tmp)
		gopath := filepath.Join(tmp, "go")
		realDir := filepath.Join(tmp, "real")
		linkDir := filepath.Join(tmp, "other", "bin")
		if err := os.MkdirAll(realDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(linkDir, 0755); err != nil {
			t.Fatal(err)
		}
		realPath := filepath.Join(realDir, "sense")
		if err := os.WriteFile(realPath, []byte("fake"), 0755); err != nil {
			t.Fatal(err)
		}
		linkPath := filepath.Join(linkDir, "sense")
		if err := os.Symlink(realPath, linkPath); err != nil {
			t.Fatal(err)
		}

		orig := osExecutable
		osExecutable = func() (string, error) { return linkPath, nil }
		t.Cleanup(func() { osExecutable = orig })
		t.Setenv("GOPATH", gopath)

		if isGoInstall() {
			t.Error("expected false for symlink outside GOPATH")
		}
	})

	t.Run("gopath env override", func(t *testing.T) {
		tmp := t.TempDir()
		tmp, _ = filepath.EvalSymlinks(tmp)
		customGopath := filepath.Join(tmp, "custom")
		binDir := filepath.Join(customGopath, "bin")
		if err := os.MkdirAll(binDir, 0755); err != nil {
			t.Fatal(err)
		}
		exePath := filepath.Join(binDir, "sense")
		if err := os.WriteFile(exePath, []byte("fake"), 0755); err != nil {
			t.Fatal(err)
		}

		orig := osExecutable
		osExecutable = func() (string, error) { return exePath, nil }
		t.Cleanup(func() { osExecutable = orig })
		t.Setenv("GOPATH", customGopath)

		if !isGoInstall() {
			t.Error("expected true when GOPATH env is set")
		}
	})

	t.Run("os executable error", func(t *testing.T) {
		orig := osExecutable
		osExecutable = func() (string, error) { return "", errors.New("boom") }
		t.Cleanup(func() { osExecutable = orig })

		if isGoInstall() {
			t.Error("expected false when os.Executable fails")
		}
	})

	t.Run("eval symlinks error", func(t *testing.T) {
		orig := osExecutable
		osExecutable = func() (string, error) { return "/nonexistent/path/sense", nil }
		t.Cleanup(func() { osExecutable = orig })

		if isGoInstall() {
			t.Error("expected false when EvalSymlinks fails")
		}
	})

	t.Run("user home dir error", func(t *testing.T) {
		tmp := t.TempDir()
		tmp, _ = filepath.EvalSymlinks(tmp)
		exePath := filepath.Join(tmp, "sense")
		if err := os.WriteFile(exePath, []byte("fake"), 0o755); err != nil {
			t.Fatal(err)
		}

		orig := osExecutable
		osExecutable = func() (string, error) { return exePath, nil }
		t.Cleanup(func() { osExecutable = orig })
		t.Setenv("GOPATH", "")
		t.Setenv("HOME", "")

		if isGoInstall() {
			t.Error("expected false when UserHomeDir fails")
		}
	})
}

func TestUpdateEarlyExits(t *testing.T) {
	tests := []struct {
		name           string
		version        string
		isGoInstall    bool
		fetchLatestTag func() (string, error)
		wantCode       int
		wantStdout     string
		wantStderr     string
	}{
		{
			name:       "dev build",
			version:    "0.0.0-dev",
			wantCode:   1,
			wantStderr: "development build",
		},
		{
			name:        "go install",
			version:     "0.1.0",
			isGoInstall: true,
			wantCode:    1,
			wantStderr:  "go install",
		},
		{
			name:           "fetch error",
			version:        "0.1.0",
			isGoInstall:    false,
			fetchLatestTag: func() (string, error) { return "", errors.New("network down") },
			wantCode:       1,
			wantStderr:     "network down",
		},
		{
			name:           "already latest",
			version:        "0.1.0",
			isGoInstall:    false,
			fetchLatestTag: func() (string, error) { return "0.1.0", nil },
			wantCode:       0,
			wantStdout:     "already the latest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origVersion := version.Version
			version.Version = tt.version
			t.Cleanup(func() { version.Version = origVersion })

			origIsGoInstall := isGoInstallFn
			isGoInstallFn = func() bool { return tt.isGoInstall }
			t.Cleanup(func() { isGoInstallFn = origIsGoInstall })

			origFetch := fetchLatestTagFn
			if tt.fetchLatestTag != nil {
				fetchLatestTagFn = tt.fetchLatestTag
			} else {
				fetchLatestTagFn = func() (string, error) { return "", errors.New("unexpected call") }
			}
			t.Cleanup(func() { fetchLatestTagFn = origFetch })

			var stdout, stderr bytes.Buffer
			code := Update(&stdout, &stderr)

			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d", code, tt.wantCode)
			}
			if tt.wantStdout != "" && !bytes.Contains(stdout.Bytes(), []byte(tt.wantStdout)) {
				t.Errorf("stdout = %q, want to contain %q", stdout.String(), tt.wantStdout)
			}
			if tt.wantStderr != "" && !bytes.Contains(stderr.Bytes(), []byte(tt.wantStderr)) {
				t.Errorf("stderr = %q, want to contain %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}

func TestUpdateNewerAvailable(t *testing.T) {
	origVersion := version.Version
	version.Version = "0.1.0"
	t.Cleanup(func() { version.Version = origVersion })

	origIsGoInstall := isGoInstallFn
	isGoInstallFn = func() bool { return false }
	t.Cleanup(func() { isGoInstallFn = origIsGoInstall })

	origFetch := fetchLatestTagFn
	fetchLatestTagFn = func() (string, error) { return "0.2.0", nil }
	t.Cleanup(func() { fetchLatestTagFn = origFetch })

	var installCalled bool
	var installVersion string
	origRunInstall := runInstallScriptFn
	runInstallScriptFn = func(ver string, _, _ io.Writer) error {
		installCalled = true
		installVersion = ver
		return nil
	}
	t.Cleanup(func() { runInstallScriptFn = origRunInstall })

	var stdout, stderr bytes.Buffer
	code := Update(&stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if !bytes.Contains(stdout.Bytes(), []byte("v0.1.0 → v0.2.0 available.")) {
		t.Errorf("stdout = %q, want to contain availability message", stdout.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte("Downloading and installing v0.2.0...")) {
		t.Errorf("stdout = %q, want to contain download message", stdout.String())
	}
	if !installCalled {
		t.Error("runInstallScript was not called")
	}
	if installVersion != "0.2.0" {
		t.Errorf("install version = %q, want %q", installVersion, "0.2.0")
	}
}

func TestUpdateInstallScriptFails(t *testing.T) {
	origVersion := version.Version
	version.Version = "0.1.0"
	t.Cleanup(func() { version.Version = origVersion })

	origIsGoInstall := isGoInstallFn
	isGoInstallFn = func() bool { return false }
	t.Cleanup(func() { isGoInstallFn = origIsGoInstall })

	origFetch := fetchLatestTagFn
	fetchLatestTagFn = func() (string, error) { return "0.2.0", nil }
	t.Cleanup(func() { fetchLatestTagFn = origFetch })

	origRunInstall := runInstallScriptFn
	runInstallScriptFn = func(string, io.Writer, io.Writer) error {
		return errors.New("install boom")
	}
	t.Cleanup(func() { runInstallScriptFn = origRunInstall })

	var stdout, stderr bytes.Buffer
	if code := Update(&stdout, &stderr); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !bytes.Contains(stderr.Bytes(), []byte("install failed: install boom")) {
		t.Errorf("stderr missing install-failed message: %q", stderr.String())
	}
}

func TestRunInstallScript(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		orig := installCommandFn
		installCommandFn = func(string) *exec.Cmd { return exec.Command("true") }
		t.Cleanup(func() { installCommandFn = orig })

		var stdout, stderr bytes.Buffer
		if err := runInstallScript("0.3.0", &stdout, &stderr); err != nil {
			t.Errorf("runInstallScript: %v", err)
		}
	})

	t.Run("command failure propagates", func(t *testing.T) {
		orig := installCommandFn
		installCommandFn = func(string) *exec.Cmd { return exec.Command("false") }
		t.Cleanup(func() { installCommandFn = orig })

		var stdout, stderr bytes.Buffer
		if err := runInstallScript("0.3.0", &stdout, &stderr); err == nil {
			t.Error("runInstallScript: expected error from exit-1 command")
		}
	})
}

func TestIsGoInstall_GopathEmptyUsesHome(t *testing.T) {
	tmp := t.TempDir()
	tmp, _ = filepath.EvalSymlinks(tmp)
	binDir := filepath.Join(tmp, "go", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	exePath := filepath.Join(binDir, "sense")
	if err := os.WriteFile(exePath, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}

	orig := osExecutable
	osExecutable = func() (string, error) { return exePath, nil }
	t.Cleanup(func() { osExecutable = orig })

	t.Setenv("GOPATH", "")
	t.Setenv("HOME", tmp)

	if !isGoInstall() {
		t.Error("expected true when GOPATH unset and HOME/go/bin matches exe")
	}
}

func TestInstallCommand(t *testing.T) {
	cmd := installCommand("0.5.0")

	if filepath.Base(cmd.Path) != "sh" {
		t.Errorf("cmd.Path basename = %q, want %q", filepath.Base(cmd.Path), "sh")
	}
	wantArgs := []string{"sh", "-c", "curl -fsSL " + installScriptURL + " | sh"}
	if len(cmd.Args) != len(wantArgs) {
		t.Fatalf("len(cmd.Args) = %d, want %d", len(cmd.Args), len(wantArgs))
	}
	for i, want := range wantArgs {
		if cmd.Args[i] != want {
			t.Errorf("cmd.Args[%d] = %q, want %q", i, cmd.Args[i], want)
		}
	}

	foundVersion := false
	for _, env := range cmd.Env {
		if env == "VERSION=0.5.0" {
			foundVersion = true
			break
		}
	}
	if !foundVersion {
		t.Errorf("cmd.Env missing VERSION=0.5.0, got %v", cmd.Env)
	}

	// Safety: never actually run the command.
	if cmd.Stdout != nil || cmd.Stderr != nil {
		t.Error("installCommand should not set Stdout/Stderr")
	}
}

func TestInGopathBin(t *testing.T) {
	tests := []struct {
		name   string
		exe    string
		gopath string
		want   bool
	}{
		{"normal install path", "/usr/local/bin/sense", "/home/user/go", false},
		{"gopath bin", "/home/user/go/bin/sense", "/home/user/go", true},
		{"similar prefix", "/home/user/go-tools/bin/sense", "/home/user/go", false},
		{"home local bin", filepath.Join("/home/user/.local/bin", "sense"), "/home/user/go", false},
		{"gopath with trailing slash", "/home/user/go/bin/sense", "/home/user/go/", true},
		{"bin dir itself", "/home/user/go/bin", "/home/user/go", false},
		{"nested in gopath bin", "/home/user/go/bin/subdir/sense", "/home/user/go", true},
		{"empty gopath", "/home/user/go/bin/sense", "", false},
		{"similar prefix to bin", "/home/user/go/bin-backup/sense", "/home/user/go", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := inGopathBin(tt.exe, tt.gopath); got != tt.want {
				t.Errorf("inGopathBin(%q, %q) = %v, want %v", tt.exe, tt.gopath, got, tt.want)
			}
		})
	}
}

func TestUpdateDevBuild(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Update(&stdout, &stderr)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !bytes.Contains(stderr.Bytes(), []byte("development build")) {
		t.Errorf("stderr = %q, expected development build message", stderr.String())
	}
}
