package cli

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/luuuc/sense/internal/config"

	_ "modernc.org/sqlite"
)

// RunStatus prints index health and embedding coverage.
func RunStatus(args []string, cio IO) int {
	senseDir := filepath.Join(cio.Dir, ".sense")
	if env := os.Getenv("SENSE_DIR"); env != "" {
		senseDir = env
	}

	dbPath := filepath.Join(senseDir, "index.db")
	if _, err := os.Stat(dbPath); err != nil {
		_, _ = fmt.Fprintln(cio.Stdout, "index: not built (run 'sense scan')")
		return ExitSuccess
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		_, _ = fmt.Fprintf(cio.Stderr, "sense status: %v\n", err)
		return ExitGeneralError
	}
	defer func() { _ = db.Close() }()

	var files, symbols, edges, embeddings int
	_ = db.QueryRow("SELECT COUNT(*) FROM sense_files").Scan(&files)
	_ = db.QueryRow("SELECT COUNT(*) FROM sense_symbols").Scan(&symbols)
	_ = db.QueryRow("SELECT COUNT(*) FROM sense_edges").Scan(&edges)
	_ = db.QueryRow("SELECT COUNT(*) FROM sense_embeddings").Scan(&embeddings)

	_, _ = fmt.Fprintf(cio.Stdout, "index:      %s\n", dbPath)
	_, _ = fmt.Fprintf(cio.Stdout, "files:      %d\n", files)
	_, _ = fmt.Fprintf(cio.Stdout, "symbols:    %d\n", symbols)
	_, _ = fmt.Fprintf(cio.Stdout, "edges:      %d\n", edges)

	enabled := EmbeddingsEnabled(cio.Dir)
	if enabled {
		coverage := 0
		if symbols > 0 {
			coverage = embeddings * 100 / symbols
		}
		_, _ = fmt.Fprintf(cio.Stdout, "embeddings: %d/%d (%d%% coverage)\n", embeddings, symbols, coverage)
	} else {
		_, _ = fmt.Fprintf(cio.Stdout, "embeddings: disabled\n")
	}
	return ExitSuccess
}

// EmbeddingsEnabled resolves whether embeddings are active by checking
// the SENSE_EMBEDDINGS env var first, then falling back to config.yml.
// Default is true (embeddings on).
func EmbeddingsEnabled(root string) bool {
	if env := os.Getenv("SENSE_EMBEDDINGS"); env != "" {
		return !strings.EqualFold(env, "false") && env != "0"
	}
	cfg, err := config.Load(root)
	if err != nil {
		return true
	}
	return cfg.EmbeddingsEnabled()
}
