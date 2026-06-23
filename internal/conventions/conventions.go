package conventions

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

const minInstances = 3
const minInterfaceInstances = 2
const maxDescriptionNames = 5
const minPrevalence = 0.10
const minPrevalenceInstances = 5

type Category string

const (
	CategoryInheritance   Category = "inheritance"
	CategoryNaming        Category = "naming"
	CategoryStructure     Category = "structure"
	CategoryComposition   Category = "composition"
	CategoryTesting       Category = "testing"
	CategoryDesignPattern Category = "design_pattern"
	CategoryFramework     Category = "framework"
	CategoryArchitecture  Category = "architecture"
	CategoryKeyTypes      Category = "key_types"
)

type Example struct {
	Name      string
	Path      string
	Kind      string
	EdgeCount int
}

type Convention struct {
	Category    Category
	Description string
	Instances   int
	Total       int
	Strength    float64
	Examples    []Example
	KeySymbol   string
	// Significance is an ordering-only weight (not shown to the caller) that
	// ranks a convention's informativeness within its category, ahead of raw
	// prevalence. It lets a project-specific sub-architecture (a namespaced base
	// like Payment::BaseProviderStrategy, or a per-client Error taxonomy) outrank
	// a generic framework base (ApplicationRecord) the caller already knows, so
	// the non-obvious conventions survive the response's token budget. Set by the
	// per-language refiners (see refineRubySignificance); zero for conventions a
	// refiner does not touch, leaving prevalence the sole tiebreak.
	Significance float64
}

type Options struct {
	Domain      string
	MinStrength float64
}

func Detect(ctx context.Context, db *sql.DB, opts Options) ([]Convention, int, error) {
	fileFilter, err := resolveFileFilter(ctx, db, opts.Domain)
	if err != nil {
		return nil, 0, err
	}

	symbols, err := loadSymbols(ctx, db, fileFilter)
	if err != nil {
		return nil, 0, err
	}
	// Drop synthetic cross-language/framework symbols (i18n keys, route helpers,
	// turbo channels, importmap entries, ruby-core shims). These are plumbing the
	// extractor emits for cross-language resolution, not declarations the project
	// wrote; on a Rails app they outnumber real symbols and would otherwise
	// dominate the naming/structure conventions. Conventions drops the full
	// synthetic set (the resolver's cross-language gate uses the same set; search
	// and dead-code drop only the ruby-core/route subset that reaches them).
	symbols = dropSyntheticSymbols(symbols)
	if len(symbols) == 0 {
		return []Convention{}, 0, nil
	}

	edges, err := loadEdges(ctx, db, fileFilter)
	if err != nil {
		return nil, 0, err
	}

	files, err := loadFiles(ctx, db, fileFilter)
	if err != nil {
		return nil, 0, err
	}

	symbolByID := indexSymbols(symbols)
	filePathByID := make(map[int64]string, len(files))
	for _, f := range files {
		filePathByID[f.id] = f.path
	}

	var conventions []Convention
	conventions = append(conventions, detectInheritance(symbols, edges, symbolByID, filePathByID)...)
	conventions = append(conventions, detectNaming(symbols, filePathByID)...)
	conventions = append(conventions, detectStructure(symbols, filePathByID)...)
	conventions = append(conventions, detectComposition(symbols, edges, symbolByID, filePathByID)...)
	conventions = append(conventions, detectTesting(symbols, edges, filePathByID, symbolByID)...)
	conventions = append(conventions, detectDesignPatterns(symbols, symbolByID, filePathByID)...)
	conventions = append(conventions, detectFrameworkIdioms(symbols, edges, symbolByID, filePathByID)...)
	conventions = append(conventions, detectArchitectureLayers(symbols, edges, symbolByID, filePathByID)...)
	conventions = append(conventions, detectExternalDependencies(ctx, db, opts.Domain, fileFilter)...)
	conventions = append(conventions, detectKeyTypes(symbols, edges, filePathByID, conventions)...)

	enrichEdgeCounts(conventions, symbols, edges, filePathByID)
	refineRubySignificance(conventions)

	sort.Slice(conventions, func(i, j int) bool {
		return conventionLess(conventions[i], conventions[j])
	})

	conventions = applyConventionFilters(conventions, opts)

	return conventions, len(symbols), nil
}

// conventionLess is the canonical ordering: by category, then descending
// strength, then description. This order is part of the tool's contract.
func conventionLess(a, b Convention) bool {
	if a.Category != b.Category {
		return categoryOrder(a.Category) < categoryOrder(b.Category)
	}
	if a.Significance != b.Significance {
		return a.Significance > b.Significance
	}
	if a.Strength != b.Strength {
		return a.Strength > b.Strength
	}
	return a.Description < b.Description
}

// applyConventionFilters drops conventions that are not prevalent, then, when
// the options ask for them, those outside the requested domain or below the
// strength floor. The filters compose in this fixed order.
func applyConventionFilters(conventions []Convention, opts Options) []Convention {
	filtered := conventions[:0]
	for _, c := range conventions {
		prevalent := c.Instances >= minPrevalenceInstances ||
			(c.Total > 0 && float64(c.Instances)/float64(c.Total) >= minPrevalence)
		if prevalent {
			filtered = append(filtered, c)
		}
	}
	conventions = filtered

	if opts.Domain != "" {
		filtered := conventions[:0]
		for _, c := range conventions {
			if hasMatchingExample(c.Examples, opts.Domain) {
				filtered = append(filtered, c)
			}
		}
		conventions = filtered
	}

	if opts.MinStrength > 0 {
		filtered := conventions[:0]
		for _, c := range conventions {
			if c.Strength >= opts.MinStrength {
				filtered = append(filtered, c)
			}
		}
		conventions = filtered
	}

	return conventions
}

type symbolRow struct {
	id        int64
	fileID    int64
	name      string
	qualified string
	kind      string
	parentID  *int64
}

type edgeRow struct {
	sourceID int64
	targetID int64
	kind     string
}

type fileRow struct {
	id   int64
	path string
}

func resolveFileFilter(ctx context.Context, db *sql.DB, domain string) ([]int64, error) {
	if domain == "" {
		return nil, nil
	}
	pattern := "%" + domain + "%"
	rows, err := db.QueryContext(ctx, `SELECT id FROM sense_files WHERE path LIKE ?`, pattern)
	if err != nil {
		return nil, fmt.Errorf("conventions: resolve domain: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func loadSymbols(ctx context.Context, db *sql.DB, fileFilter []int64) ([]symbolRow, error) {
	if len(fileFilter) == 0 {
		return querySymbols(ctx, db, `SELECT id, file_id, name, qualified, kind, parent_id FROM sense_symbols`, nil)
	}
	var out []symbolRow
	for _, chunk := range chunkIDs(fileFilter) {
		placeholders := strings.Repeat("?,", len(chunk))
		placeholders = placeholders[:len(placeholders)-1]
		q := `SELECT id, file_id, name, qualified, kind, parent_id FROM sense_symbols WHERE file_id IN (` + placeholders + `)`
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		batch, err := querySymbols(ctx, db, q, args)
		if err != nil {
			return nil, err
		}
		out = append(out, batch...)
	}
	return out, nil
}

func querySymbols(ctx context.Context, db *sql.DB, q string, args []any) ([]symbolRow, error) {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("conventions: load symbols: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []symbolRow
	for rows.Next() {
		var s symbolRow
		var parentID sql.NullInt64
		if err := rows.Scan(&s.id, &s.fileID, &s.name, &s.qualified, &s.kind, &parentID); err != nil {
			return nil, err
		}
		if parentID.Valid {
			p := parentID.Int64
			s.parentID = &p
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// loadEdges loads edges, optionally filtered to those recorded in the given
// files. Note: file_id on an edge is where the relationship was *found*, so
// domain filtering captures edges whose source declaration lives in-domain.
func loadEdges(ctx context.Context, db *sql.DB, fileFilter []int64) ([]edgeRow, error) {
	if len(fileFilter) == 0 {
		return queryEdges(ctx, db, `SELECT source_id, target_id, kind FROM sense_edges`, nil)
	}
	var out []edgeRow
	for _, chunk := range chunkIDs(fileFilter) {
		placeholders := strings.Repeat("?,", len(chunk))
		placeholders = placeholders[:len(placeholders)-1]
		q := `SELECT source_id, target_id, kind FROM sense_edges WHERE file_id IN (` + placeholders + `)`
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		batch, err := queryEdges(ctx, db, q, args)
		if err != nil {
			return nil, err
		}
		out = append(out, batch...)
	}
	return out, nil
}

func queryEdges(ctx context.Context, db *sql.DB, q string, args []any) ([]edgeRow, error) {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("conventions: load edges: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []edgeRow
	for rows.Next() {
		var (
			e        edgeRow
			sourceID sql.NullInt64
		)
		if err := rows.Scan(&sourceID, &e.targetID, &e.kind); err != nil {
			return nil, err
		}
		e.sourceID = sourceID.Int64 // 0 when NULL; convention detectors skip unknown IDs
		out = append(out, e)
	}
	return out, rows.Err()
}

func loadFiles(ctx context.Context, db *sql.DB, fileFilter []int64) ([]fileRow, error) {
	if len(fileFilter) == 0 {
		return queryFiles(ctx, db, `SELECT id, path FROM sense_files`, nil)
	}
	var out []fileRow
	for _, chunk := range chunkIDs(fileFilter) {
		placeholders := strings.Repeat("?,", len(chunk))
		placeholders = placeholders[:len(placeholders)-1]
		q := `SELECT id, path FROM sense_files WHERE id IN (` + placeholders + `)`
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		batch, err := queryFiles(ctx, db, q, args)
		if err != nil {
			return nil, err
		}
		out = append(out, batch...)
	}
	return out, nil
}

func queryFiles(ctx context.Context, db *sql.DB, q string, args []any) ([]fileRow, error) {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("conventions: load files: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []fileRow
	for rows.Next() {
		var f fileRow
		if err := rows.Scan(&f.id, &f.path); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

const maxSQLiteVars = 500

func chunkIDs(ids []int64) [][]int64 {
	if len(ids) <= maxSQLiteVars {
		return [][]int64{ids}
	}
	var chunks [][]int64
	for i := 0; i < len(ids); i += maxSQLiteVars {
		end := i + maxSQLiteVars
		if end > len(ids) {
			end = len(ids)
		}
		chunks = append(chunks, ids[i:end])
	}
	return chunks
}
