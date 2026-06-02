package eval

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/luuuc/sense/internal/dead"
	"github.com/luuuc/sense/internal/scan"
)

// The cargo oracle is the Rust voice's compiler-grade trust gate. rustc's
// `dead_code` lint flags exactly Sense's earned-`dead` class — unused non-`pub`
// items — but with the full type system, trait resolution, and macro expansion
// Sense lacks. So the binding invariant is a SUBSET, not equality:
//
//	Sense `dead` (Rust) ⊆ cargo `dead_code`
//
// A Sense `dead` symbol that cargo does NOT flag is a false `dead` and fails the
// gate (precision is the only unforgivable error, per 25-13). The reverse — cargo
// flags more than Sense — is acceptable lost recall, since Sense fails closed on
// anything it cannot prove (an unbound mention, an un-harvested language, a `pub`
// item, a trait/derive/FFI/test idiom).

// rustSymRef identifies a Rust symbol by repo-relative file and bare name — the
// join key between Sense's `dead` set and cargo's dead_code findings. Keyed on
// name+file rather than line/column so the two tools' slightly different position
// reporting never causes a spurious mismatch.
type rustSymRef struct {
	File string
	Name string
}

// DeadCodeSet is the set of symbols cargo flags as dead (the dead_code lint).
type DeadCodeSet map[rustSymRef]struct{}

// RustDead is one symbol Sense earns the `dead` verdict for, carrying the fields
// needed to join against the oracle and to report a violation legibly.
type RustDead struct {
	Qualified string
	File      string
	Name      string
}

// deadCodeNameRe pulls the backtick-quoted identifier(s) out of a rustc dead_code
// message, e.g. "function `foo` is never used", "associated function `new` is
// never used", "struct `Bar` is never constructed". A grouped message ("methods
// `a` and `b` are never used") yields each name; all share the diagnostic's file.
var deadCodeNameRe = regexp.MustCompile("`([A-Za-z_][A-Za-z0-9_]*)`")

// cargoMessage is the minimal shape of one `--message-format=json` line: cargo
// wraps each rustc diagnostic as {"reason":"compiler-message","message":{…}}. We
// read only the dead_code diagnostics' message text and primary span file.
type cargoMessage struct {
	Reason  string `json:"reason"`
	Message *struct {
		Message string `json:"message"`
		Code    *struct {
			Code string `json:"code"`
		} `json:"code"`
		Spans []struct {
			FileName  string `json:"file_name"`
			IsPrimary bool   `json:"is_primary"`
		} `json:"spans"`
	} `json:"message"`
}

// ParseCargoDeadCode parses `cargo check --message-format=json` output into the
// set of flagged (file, bare-name) symbols. Non-JSON lines and non-dead_code
// diagnostics are ignored, so the parser tolerates the build/progress noise cargo
// interleaves on the same stream.
func ParseCargoDeadCode(output string) DeadCodeSet {
	set := DeadCodeSet{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] != '{' {
			continue
		}
		var m cargoMessage
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			continue
		}
		if m.Reason != "compiler-message" || m.Message == nil {
			continue
		}
		if m.Message.Code == nil || m.Message.Code.Code != "dead_code" {
			continue
		}
		file := primarySpanFile(m)
		if file == "" {
			continue
		}
		for _, nm := range deadCodeNameRe.FindAllStringSubmatch(m.Message.Message, -1) {
			set[rustSymRef{File: normalizeRel(file), Name: nm[1]}] = struct{}{}
		}
	}
	return set
}

// primarySpanFile returns the file of the diagnostic's primary span, falling back
// to the first span when none is marked primary.
func primarySpanFile(m cargoMessage) string {
	if m.Message == nil || len(m.Message.Spans) == 0 {
		return ""
	}
	for _, s := range m.Message.Spans {
		if s.IsPrimary {
			return s.FileName
		}
	}
	return m.Message.Spans[0].FileName
}

// ErrCargoUnavailable is returned by RunCargoDeadCode when the cargo binary cannot
// be launched, so callers can skip rather than fail.
var ErrCargoUnavailable = fmt.Errorf("cargo unavailable")

// runCargo launches `cargo check --message-format=json` in repoRoot and returns
// its combined output. It is a package var so tests can inject canned output and
// error shapes deterministically, without depending on the toolchain. `check`
// (not `build`) is enough for the dead_code lint and far faster.
var runCargo = func(ctx context.Context, repoRoot string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "cargo", "check", "--message-format=json")
	cmd.Dir = repoRoot
	return cmd.CombinedOutput()
}

// RunCargoDeadCode runs cargo in repoRoot and returns the parsed dead_code set.
// cargo exits non-zero on a compile error but still streams the diagnostics it
// produced, so an ExitError with parseable output is success; only a failure to
// launch the binary (e.g. not installed) is fatal. The caller is expected to skip
// when ErrCargoUnavailable is returned.
//
// FRESHNESS: cargo only emits diagnostics for crates it (re)compiles. On an
// already-built target an up-to-date `cargo check` recompiles nothing and returns
// an EMPTY set — which reads as "no dead code" but means "nothing rebuilt". A
// caller validating a real repo must force a fresh build first (`cargo clean`, a
// throwaway `CARGO_TARGET_DIR`, or touching the workspace sources). The temp-crate
// callers in this package are always fresh, so they need no such step.
func RunCargoDeadCode(ctx context.Context, repoRoot string) (DeadCodeSet, error) {
	out, err := runCargo(ctx, repoRoot)
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return nil, fmt.Errorf("%w: %v", ErrCargoUnavailable, err)
		}
	}
	return ParseCargoDeadCode(string(out)), nil
}

// RustDeadSymbols scans repoRoot with Sense and returns the Rust symbols it earns
// the `dead` verdict for — the left side of the subset gate.
func RustDeadSymbols(ctx context.Context, repoRoot, senseDir string) ([]RustDead, error) {
	if _, err := scan.Run(ctx, scan.Options{
		Root:     repoRoot,
		Sense:    senseDir,
		Output:   &bytes.Buffer{},
		Warnings: io.Discard,
	}); err != nil {
		return nil, fmt.Errorf("scan %s: %w", repoRoot, err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(senseDir, "index.db"))
	if err != nil {
		return nil, fmt.Errorf("open index: %w", err)
	}
	defer func() { _ = db.Close() }()

	res, err := dead.FindDead(ctx, db, dead.Options{Language: "rust", Limit: 1000000})
	if err != nil {
		return nil, fmt.Errorf("find dead: %w", err)
	}
	var out []RustDead
	for _, f := range res.Findings {
		if f.Verdict != dead.VerdictDead {
			continue
		}
		out = append(out, RustDead{
			Qualified: f.Symbol.Qualified,
			File:      normalizeRel(f.Symbol.File),
			Name:      f.Symbol.Name,
		})
	}
	return out, nil
}

// RustFalseDeads returns the Sense `dead` Rust symbols absent from the cargo
// dead_code set — the gate violations. An empty result is the only passing state:
// every symbol Sense called `dead` is one cargo independently agrees is unused.
// Order is stable for legible reporting.
func RustFalseDeads(senseDead []RustDead, oracle DeadCodeSet) []RustDead {
	var out []RustDead
	for _, d := range senseDead {
		if _, ok := oracle[rustSymRef{File: d.File, Name: d.Name}]; !ok {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Name < out[j].Name
	})
	return out
}
