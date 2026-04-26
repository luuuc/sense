package versioncheck

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/luuuc/sense/internal/version"
)

const installScriptURL = "https://raw.githubusercontent.com/" + repo + "/main/install.sh"

func Update(stdout, stderr io.Writer) int {
	if version.Version == "0.0.0-dev" {
		_, _ = fmt.Fprintln(stderr, "sense update: running a development build — update is not available.")
		return 1
	}

	if isGoInstall() {
		_, _ = fmt.Fprintln(stderr, "sense update: you installed via `go install`. Run:\n  go install github.com/luuuc/sense/cmd/sense@latest\nOr switch to the install script:\n  curl -fsSL "+installScriptURL+" | sh")
		return 1
	}

	_, _ = fmt.Fprintln(stdout, "Checking for updates...")

	latest, err := fetchLatestTag()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "sense update: %v\n", err)
		return 1
	}

	if !isNewer(latest, version.Version) {
		_, _ = fmt.Fprintf(stdout, "sense v%s is already the latest version.\n", version.Version)
		return 0
	}

	_, _ = fmt.Fprintf(stdout, "v%s → v%s available.\n", version.Version, latest)
	_, _ = fmt.Fprintln(stdout, "Downloading and installing v"+latest+"...")

	if err := runInstallScript(latest, stdout, stderr); err != nil {
		_, _ = fmt.Fprintf(stderr, "sense update: install failed: %v\n", err)
		return 1
	}

	_, _ = fmt.Fprintln(stdout, "Updated successfully. Run 'sense version' to confirm.")
	return 0
}

func runInstallScript(ver string, stdout, stderr io.Writer) error {
	curlCmd := fmt.Sprintf("curl -fsSL %s | sh", installScriptURL)

	cmd := exec.Command("sh", "-c", curlCmd)
	cmd.Env = append(os.Environ(), "VERSION="+ver)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func isGoInstall() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return false
	}

	gopath := os.Getenv("GOPATH")
	if gopath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return false
		}
		gopath = filepath.Join(home, "go")
	}

	return inGopathBin(exe, gopath)
}

func inGopathBin(exe, gopath string) bool {
	gopathBin := filepath.Join(gopath, "bin") + string(filepath.Separator)
	return strings.HasPrefix(exe, gopathBin)
}
