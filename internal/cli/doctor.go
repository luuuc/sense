package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/luuuc/sense/internal/embed"
	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/sqlite"
)

const doctorHelp = `usage: sense doctor [flags]

Run diagnostic checks on the index and report pass/warn/fail per check.

Flags:
  --json       Emit JSON output
  -h, --help   Show this help

Exit codes:
  0  all checks pass (warnings are OK)
  1  at least one check failed
`

type checkResult struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Message    string `json:"message"`
	Suggestion string `json:"suggestion,omitempty"`
}

type doctorResponse struct {
	Checks      []checkResult `json:"checks"`
	Suggestions []string      `json:"suggestions"`
}

func RunDoctor(args []string, cio IO) int {
	fs := flag.NewFlagSet("sense doctor", flag.ContinueOnError)
	fs.SetOutput(cio.Stderr)
	jsonFlag := fs.Bool("json", false, "")
	fs.Usage = func() { _, _ = fmt.Fprint(cio.Stderr, doctorHelp) }
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitSuccess
		}
		return ExitGeneralError
	}

	ctx := context.Background()
	resp := runDoctorChecks(ctx, cio)

	if *jsonFlag {
		out, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			_, _ = fmt.Fprintf(cio.Stderr, "sense doctor: %v\n", err)
			return ExitGeneralError
		}
		_, _ = fmt.Fprintln(cio.Stdout, string(out))
	} else {
		renderDoctorHuman(cio, resp)
	}

	for _, c := range resp.Checks {
		if c.Status == "fail" {
			return ExitGeneralError
		}
	}
	return ExitSuccess
}

func runDoctorChecks(ctx context.Context, cio IO) doctorResponse {
	var checks []checkResult

	senseDir := filepath.Join(cio.Dir, ".sense")
	if env := os.Getenv("SENSE_DIR"); env != "" {
		senseDir = env
	}
	dbPath := filepath.Join(senseDir, "index.db")

	// Check 1: Index exists
	if _, err := os.Stat(dbPath); err != nil {
		checks = append(checks, checkResult{
			Name:       "index_exists",
			Status:     "fail",
			Message:    "Index not found",
			Suggestion: "Run `sense scan` to build the index",
		})
		return builddoctorResponse(checks)
	}
	checks = append(checks, checkResult{
		Name:    "index_exists",
		Status:  "pass",
		Message: fmt.Sprintf("Index exists (%s)", dbPath),
	})

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		checks = append(checks, checkResult{
			Name:       "schema_version",
			Status:     "fail",
			Message:    fmt.Sprintf("Cannot open index: %v", err),
			Suggestion: "Run `sense scan --force` to rebuild",
		})
		return builddoctorResponse(checks)
	}
	defer func() { _ = db.Close() }()

	// Check 2: Schema version
	var schemaVer int
	_ = db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&schemaVer)
	if schemaVer == sqlite.SchemaVersion {
		checks = append(checks, checkResult{
			Name:    "schema_version",
			Status:  "pass",
			Message: fmt.Sprintf("Schema version current (v%d)", schemaVer),
		})
	} else {
		checks = append(checks, checkResult{
			Name:       "schema_version",
			Status:     "fail",
			Message:    fmt.Sprintf("Schema version mismatch (index: v%d, binary: v%d)", schemaVer, sqlite.SchemaVersion),
			Suggestion: "Run `sense scan --force` to rebuild",
		})
	}

	// Check 3: Embedding model
	storedModel := readMeta(ctx, db, "embedding_model")
	if storedModel == "" || storedModel == embed.ModelID {
		checks = append(checks, checkResult{
			Name:    "embedding_model",
			Status:  "pass",
			Message: fmt.Sprintf("Embedding model matches (%s)", embed.ModelID),
		})
	} else {
		checks = append(checks, checkResult{
			Name:       "embedding_model",
			Status:     "fail",
			Message:    fmt.Sprintf("Embedding model mismatch (index: %s, binary: %s)", storedModel, embed.ModelID),
			Suggestion: "Run `sense scan --force` to re-embed with the new model",
		})
	}

	// Check 4: Stale files
	staleCount := countStaleFilesCLI(ctx, db, cio.Dir)
	switch {
	case staleCount == 0:
		checks = append(checks, checkResult{
			Name:    "stale_files",
			Status:  "pass",
			Message: "No stale files",
		})
	case staleCount <= 10:
		checks = append(checks, checkResult{
			Name:       "stale_files",
			Status:     "warn",
			Message:    fmt.Sprintf("%d stale files (modified since last scan)", staleCount),
			Suggestion: "Run `sense scan` to re-index stale files",
		})
	default:
		checks = append(checks, checkResult{
			Name:       "stale_files",
			Status:     "fail",
			Message:    fmt.Sprintf("%d stale files (modified since last scan)", staleCount),
			Suggestion: "Run `sense scan` to re-index",
		})
	}

	// Check 5: Orphaned edges
	orphanCount := countOrphanedEdges(ctx, db)
	if orphanCount == 0 {
		checks = append(checks, checkResult{
			Name:    "orphaned_edges",
			Status:  "pass",
			Message: "No corrupt symbols (0 orphaned edges)",
		})
	} else {
		checks = append(checks, checkResult{
			Name:       "orphaned_edges",
			Status:     "fail",
			Message:    fmt.Sprintf("%d orphaned edges pointing to missing symbols", orphanCount),
			Suggestion: "Index may be corrupt, run `sense scan --force`",
		})
	}

	// Check 6: Language coverage
	unknownExts := findUnknownExtensions(ctx, db)
	if len(unknownExts) == 0 {
		checks = append(checks, checkResult{
			Name:    "language_coverage",
			Status:  "pass",
			Message: "All languages have extractors",
		})
	} else {
		checks = append(checks, checkResult{
			Name:    "language_coverage",
			Status:  "warn",
			Message: fmt.Sprintf("Unknown extensions found: %s", strings.Join(unknownExts, ", ")),
		})
	}

	// Check 7: Embedding completeness
	var symbols, embeddings int
	_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sense_symbols").Scan(&symbols)
	_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sense_embeddings").Scan(&embeddings)

	enabled := EmbeddingsEnabled(cio.Dir)
	if !enabled {
		checks = append(checks, checkResult{
			Name:    "embedding_completeness",
			Status:  "pass",
			Message: "Embeddings disabled (skipped)",
		})
	} else if symbols == 0 {
		checks = append(checks, checkResult{
			Name:    "embedding_completeness",
			Status:  "pass",
			Message: "No symbols to embed",
		})
	} else {
		pct := embeddings * 100 / symbols
		switch {
		case pct == 100:
			checks = append(checks, checkResult{
				Name:    "embedding_completeness",
				Status:  "pass",
				Message: fmt.Sprintf("Embeddings enabled and complete (%d/%d)", embeddings, symbols),
			})
		case pct >= 90:
			checks = append(checks, checkResult{
				Name:    "embedding_completeness",
				Status:  "warn",
				Message: fmt.Sprintf("Embeddings incomplete (%d/%d, %d%%)", embeddings, symbols, pct),
			})
		default:
			checks = append(checks, checkResult{
				Name:       "embedding_completeness",
				Status:     "fail",
				Message:    fmt.Sprintf("Embeddings incomplete (%d/%d, %d%%)", embeddings, symbols, pct),
				Suggestion: "Run `sense scan` to generate missing embeddings",
			})
		}
	}

	return builddoctorResponse(checks)
}

func builddoctorResponse(checks []checkResult) doctorResponse {
	var suggestions []string
	for _, c := range checks {
		if c.Suggestion != "" {
			suggestions = append(suggestions, c.Suggestion)
		}
	}
	return doctorResponse{Checks: checks, Suggestions: suggestions}
}

func countOrphanedEdges(ctx context.Context, db *sql.DB) int {
	const q = `SELECT COUNT(*) FROM sense_edges e
	           WHERE NOT EXISTS (SELECT 1 FROM sense_symbols s WHERE s.id = e.source_id)
	              OR NOT EXISTS (SELECT 1 FROM sense_symbols s WHERE s.id = e.target_id)`
	var count int
	_ = db.QueryRowContext(ctx, q).Scan(&count)
	return count
}

func findUnknownExtensions(ctx context.Context, db *sql.DB) []string {
	const q = `SELECT DISTINCT language FROM sense_files ORDER BY language`
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()

	registered := extract.Languages()
	regSet := make(map[string]struct{}, len(registered))
	for _, l := range registered {
		regSet[l] = struct{}{}
	}

	var unknown []string
	for rows.Next() {
		var lang string
		if err := rows.Scan(&lang); err != nil {
			continue
		}
		if _, ok := regSet[lang]; !ok {
			unknown = append(unknown, lang)
		}
	}
	sort.Strings(unknown)
	return unknown
}

func renderDoctorHuman(cio IO, resp doctorResponse) {
	w := cio.Stdout
	for _, c := range resp.Checks {
		var icon string
		switch c.Status {
		case "pass":
			icon = "\u2713"
		case "warn":
			icon = "\u26a0"
		case "fail":
			icon = "\u2717"
		}
		_, _ = fmt.Fprintf(w, "%s %s\n", icon, c.Message)
	}
	if len(resp.Suggestions) > 0 {
		_, _ = fmt.Fprintf(w, "\nSuggestions:\n")
		for _, s := range resp.Suggestions {
			_, _ = fmt.Fprintf(w, "  \u2022 %s\n", s)
		}
	}
}

