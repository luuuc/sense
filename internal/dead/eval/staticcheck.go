package eval

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// The staticcheck oracle is the Go voice's compiler-grade trust gate. `staticcheck
// -checks U1000` flags exactly Sense's earned-`dead` class — unused UNEXPORTED
// funcs / methods / types — but with the full type system and build-tag knowledge
// Sense lacks. So the binding invariant is a SUBSET, not equality:
//
//	Sense `dead` (Go) ⊆ staticcheck U1000
//
// A Sense `dead` symbol that staticcheck does NOT flag is a false `dead` and
// fails the gate (precision is the only unforgivable error, per 25-13). The
// reverse — staticcheck flags more than Sense — is acceptable lost recall, since
// Sense fails closed on anything it cannot prove (an unbound mention, an
// un-harvested language, a const/var, an interface name).

// goSymRef identifies a Go symbol by the shared join key (see symRef in
// gate.go).
type goSymRef = symRef

// U1000Set is the set of symbols staticcheck flags as unused (U1000).
type U1000Set map[goSymRef]struct{}

// GoDead is one Go symbol Sense earns the `dead` verdict for (see DeadSymbol in
// gate.go).
type GoDead = DeadSymbol

// staticcheckU1000Re matches one U1000 finding line, e.g.
//
//	internal/foo/bar.go:12:6: func unusedHelper is unused (U1000)
//	internal/foo/bar.go:20:1: func (*T).deadMethod is unused (U1000)
//	types.go:5:6: type unusedType is unused (U1000)
//
// Group 1 is the file path; group 2 is the (possibly receiver-qualified) symbol
// name, normalised to its bare last segment by bareGoName.
var staticcheckU1000Re = regexp.MustCompile(`^(.+\.go):\d+:\d+:\s+(?:func|type|const|var|field)\s+(\S+)\s+is unused \(U1000\)`)

// ParseStaticcheckU1000 parses `staticcheck -checks U1000` output into the set of
// flagged (file, bare-name) symbols. Lines that are not U1000 findings are
// ignored, so the parser tolerates the build/warning noise staticcheck mixes in.
func ParseStaticcheckU1000(output string) U1000Set {
	set := U1000Set{}
	for _, line := range strings.Split(output, "\n") {
		m := staticcheckU1000Re.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		set[goSymRef{File: normalizeRel(m[1]), Name: bareGoName(m[2])}] = struct{}{}
	}
	return set
}

// bareGoName strips a method's receiver qualifier so the name matches Sense's
// bare symbol name: `(*T).deadMethod` / `(T).deadMethod` → `deadMethod`; a plain
// `unusedHelper` is returned unchanged.
func bareGoName(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i+1:]
	}
	return name
}

// normalizeRel canonicalises a repo-relative path for joining: forward slashes,
// no leading `./`.
func normalizeRel(path string) string {
	return strings.TrimPrefix(filepath.ToSlash(path), "./")
}

// ErrStaticcheckUnavailable is returned by RunStaticcheckU1000 when the
// staticcheck binary cannot be launched, so callers can skip rather than fail.
var ErrStaticcheckUnavailable = fmt.Errorf("staticcheck unavailable")

// runStaticcheck launches `staticcheck -checks U1000 ./...` in repoRoot and
// returns its combined output. It is a package var so tests can inject canned
// output and error shapes deterministically, without depending on the binary.
var runStaticcheck = func(ctx context.Context, repoRoot string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "staticcheck", "-checks", "U1000", "./...")
	cmd.Dir = repoRoot
	return cmd.CombinedOutput()
}

// RunStaticcheckU1000 runs staticcheck in repoRoot and returns the parsed U1000
// set. staticcheck exits non-zero precisely when it reports findings, so an
// ExitError with parseable output is success; only a failure to launch the
// binary (e.g. not installed) is fatal. The caller is expected to skip when
// ErrStaticcheckUnavailable is returned.
func RunStaticcheckU1000(ctx context.Context, repoRoot string) (U1000Set, error) {
	out, err := runStaticcheck(ctx, repoRoot)
	if err != nil {
		// An ExitError means staticcheck ran and found issues (or build errors in
		// the target) — its stdout still carries the U1000 findings. Anything else
		// is a launch failure (binary missing), which is fatal.
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return nil, fmt.Errorf("%w: %v", ErrStaticcheckUnavailable, err)
		}
	}
	return ParseStaticcheckU1000(string(out)), nil
}

// GoDeadSymbols scans repoRoot with Sense and returns the Go symbols it earns the
// `dead` verdict for — the left side of the subset gate.
func GoDeadSymbols(ctx context.Context, repoRoot, senseDir string) ([]GoDead, error) {
	return deadSymbols(ctx, repoRoot, senseDir, "go")
}

// GoFalseDeads returns the Sense `dead` Go symbols absent from the staticcheck
// U1000 set — the gate violations. An empty result is the only passing state: it
// means every symbol Sense called `dead` is one staticcheck independently agrees
// is unused. Order is stable for legible reporting.
func GoFalseDeads(senseDead []GoDead, oracle U1000Set) []GoDead {
	return falseDeads(senseDead, oracle)
}
