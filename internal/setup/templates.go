package setup

import (
	"os"
	"path/filepath"
)

// templateFile is one file Sense materializes from an embedded template
// (an agent or skill definition).
type templateFile struct {
	filename string
	content  string
}

// writeTemplateFiles writes files into .claude/<sub>/. Existing files are
// overwritten to pick up template changes on re-run.
func writeTemplateFiles(root, sub string, files []templateFile) (int, error) {
	dir := filepath.Join(root, ".claude", sub)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}

	written := 0
	for _, f := range files {
		path := filepath.Join(dir, f.filename)
		if err := os.WriteFile(path, []byte(f.content), 0o644); err != nil {
			return written, err
		}
		written++
	}
	return written, nil
}
