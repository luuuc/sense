package scan

// gomodules.go collects the repo's go.mod module table for the resolver's Go
// path lane. Collection runs from DISK at resolve time (not from the changed
// file set), so the incremental pipeline sees the same table as a full scan;
// the walk mirrors collectPaths' skip rules (dot-dirs, the ignore matcher) so
// fixture and testdata go.mod files never enter the table (a fixture module
// would make third-party imports dir-bind into fixture code).

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/luuuc/sense/internal/resolve"
)

// collectGoModules walks the root for go.mod files and parses their module
// paths. Errors shrink the table instead of failing the scan: a missing
// module means the path lane stays inert for its imports (today's behavior),
// which is the sound degradation direction. File reads happen after the walk
// so no filesystem operation runs inside the callback.
func (h *harness) collectGoModules() []resolve.GoModule {
	var rels []string
	_ = filepath.WalkDir(h.root, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return nil
		}
		if cerr := h.ctx.Err(); cerr != nil {
			return cerr
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		rel, relErr := filepath.Rel(h.root, p)
		if relErr != nil {
			rel = p
		}
		if d.IsDir() {
			if p == h.root {
				return nil
			}
			if strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			if h.matcher.Match(rel, true) {
				return fs.SkipDir
			}
			return nil
		}
		if d.Name() == "go.mod" && !h.matcher.Match(rel, false) {
			rels = append(rels, rel)
		}
		return nil
	})
	var mods []resolve.GoModule
	for _, rel := range rels {
		data, err := os.ReadFile(filepath.Join(h.root, rel))
		if err != nil {
			continue
		}
		if mp := goModModulePath(data); mp != "" {
			mods = append(mods, resolve.GoModule{Path: mp, Dir: filepath.ToSlash(filepath.Dir(rel))})
		}
	}
	return mods
}

// goModModulePath extracts the module path from go.mod content: the first
// `module <path>` directive, comments and optional quotes stripped.
func goModModulePath(data []byte) string {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		rest, ok := strings.CutPrefix(line, "module")
		if !ok {
			continue
		}
		if rest == "" || (rest[0] != ' ' && rest[0] != '\t') {
			continue
		}
		if i := strings.Index(rest, "//"); i >= 0 {
			rest = rest[:i]
		}
		rest = strings.Trim(strings.TrimSpace(rest), `"`)
		if rest != "" {
			return rest
		}
	}
	return ""
}
