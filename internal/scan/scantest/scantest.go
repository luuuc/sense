// Package scantest drives the real scan pipeline against a throwaway temp
// repository and index, so a test can assert on what a scan derives without a
// checked-in fixture repo or ONNX. It is deliberately small — a temp-repo
// builder and a scan driver, nothing more. If it grows modes, options structs,
// or a fluent builder it has become the framework this cycle exists to avoid;
// keep it near 100 lines.
package scantest

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/sqlite"
)

// Repo is a temp working tree under t.TempDir(). Build one with NewRepo, scan
// it with Scan; the temp dir and any opened index close via t.Cleanup.
type Repo struct {
	Root string
	t    *testing.T
}

// NewRepo writes files (relative path → content) into a fresh t.TempDir and
// returns the repo. Parent directories are created as needed.
func NewRepo(t *testing.T, files map[string]string) *Repo {
	t.Helper()
	r := &Repo{Root: t.TempDir(), t: t}
	for rel, content := range files {
		r.Write(rel, content)
	}
	return r
}

// Write adds or replaces a file in the repo. Use between scans to drive the
// incremental path.
func (r *Repo) Write(rel, content string) {
	r.t.Helper()
	full := filepath.Join(r.Root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		r.t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		r.t.Fatalf("write %s: %v", rel, err)
	}
}

// Scan runs the real pipeline against the repo and returns the result plus an
// adapter opened on the resulting index. Root, Output, and Warnings default to
// the repo root and discarded sinks; any other field the caller sets on opts
// (EmbeddingsEnabled, Embed, Rebuild) is honored. The adapter is registered for
// cleanup, so the caller may query it directly and need not close it.
//
// To exercise the embedding path without ONNX, override the embedder seam with
// scan.SetEmbedderFactory before calling Scan (only the scan_test package can,
// which is the point — the production embedder stays the default).
func (r *Repo) Scan(opts scan.Options) (*scan.Result, *sqlite.Adapter) {
	r.t.Helper()
	opts.Root = r.Root
	if opts.Output == nil {
		opts.Output = &bytes.Buffer{}
	}
	if opts.Warnings == nil {
		opts.Warnings = io.Discard
	}

	ctx := context.Background()
	res, err := scan.Run(ctx, opts)
	if err != nil {
		r.t.Fatalf("scan.Run: %v", err)
	}

	adapter, err := sqlite.Open(ctx, filepath.Join(r.Root, ".sense", "index.db"))
	if err != nil {
		r.t.Fatalf("open index: %v", err)
	}
	r.t.Cleanup(func() { _ = adapter.Close() })
	return res, adapter
}

// Commit is one scripted commit in a WithGitHistory replay: the files to
// write (relative path → content) before committing, and the message.
// Files in the same commit co-change; temporal coupling only pairs files
// in different directories, so a fixture that wants an edge must spread
// the pair across directories.
type Commit struct {
	Files   map[string]string
	Message string
}

// WithGitHistory runs a real `git init` at the repo root and replays the
// given commits in order, so temporal-coupling code runs against genuine
// `git log` output rather than a stub. Every source of nondeterminism is
// pinned per-command (never via the developer's global config): author and
// committer identity, author and committer date (set together — they
// differ as env vars and a missing committer date is a classic flake), a
// fixed TZ, and isolation from ~/.gitconfig and the system config. The
// co-change structure is therefore identical across machines and CI
// runners. Commit timestamps are anchored to the wall clock (one hour
// apart, ending at roughly now) so the history stays inside temporal.go's
// `--since=6 months ago` window on any run date; commit SHAs consequently
// vary run-to-run and are never asserted. Skips the test if git is absent.
func (r *Repo) WithGitHistory(commits []Commit) {
	r.t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		r.t.Skip("git not available")
	}

	r.git(gitEnv(time.Time{}), "init")

	base := time.Now().UTC().Add(-time.Duration(len(commits)) * time.Hour)
	for i, c := range commits {
		for rel, content := range c.Files {
			r.Write(rel, content)
		}
		env := gitEnv(base.Add(time.Duration(i) * time.Hour))
		r.git(env, "add", "-A")
		r.git(env, "commit", "-m", c.Message)
	}
}

// git runs `git <args>` in the repo root with the given environment,
// failing the test on a non-zero exit. CombinedOutput surfaces git's own
// diagnostics in the failure message.
func (r *Repo) git(env []string, args ...string) {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.Root
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// gitEnv builds a fully-pinned git environment: fixed identity, both date
// vars stamped to date (zero date means "leave the commit date to git",
// used for `init` which writes no commit), a fixed TZ, and global/system
// config redirected to the null device so no developer or CI machine
// setting can perturb the result.
func gitEnv(date time.Time) []string {
	env := append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_SYSTEM="+os.DevNull,
		"GIT_AUTHOR_NAME=sense-test",
		"GIT_AUTHOR_EMAIL=test@sense.test",
		"GIT_COMMITTER_NAME=sense-test",
		"GIT_COMMITTER_EMAIL=test@sense.test",
		"TZ=UTC",
	)
	if !date.IsZero() {
		stamp := date.Format(time.RFC3339)
		env = append(env, "GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp)
	}
	return env
}
