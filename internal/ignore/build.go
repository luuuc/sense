package ignore

import (
	"io/fs"
	"path/filepath"
)

// Build walks root to discover .gitignore files at every directory level,
// loads the root .senseignore, and layers the given extra patterns on top.
// The result is a single Matcher that respects nested .gitignore rules the
// same way git does: a .gitignore in sub/dir/ applies to paths under sub/dir/.
func Build(root string, extra []string) (*Matcher, error) {
	m := New(defaultPatterns...)

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return nil // best-effort: skip unreadable dirs
		}
		if !d.IsDir() {
			return nil
		}
		// Skip the .sense directory itself.
		if d.Name() == ".sense" && path != root {
			return fs.SkipDir
		}

		gi := filepath.Join(path, ".gitignore")
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		prefix := ""
		if rel != "." {
			prefix = filepath.ToSlash(rel)
		}
		if err := m.AddFromFile(gi, prefix); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// .senseignore at root — same syntax, additional layer.
	if err := m.AddFromFile(filepath.Join(root, ".senseignore"), ""); err != nil {
		return nil, err
	}

	// Config-level patterns go last so they override everything.
	m.Add(extra...)

	return m, nil
}
