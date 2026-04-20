package conventions

import (
	"context"
	"database/sql"
	"fmt"
	"path"
	"sort"
	"strings"
)

const minInstances = 3

type Category string

const (
	CategoryInheritance  Category = "inheritance"
	CategoryNaming       Category = "naming"
	CategoryStructure    Category = "structure"
	CategoryComposition  Category = "composition"
	CategoryTesting      Category = "testing"
)

type Convention struct {
	Category    Category
	Description string
	Instances   int
	Total       int
	Strength    float64
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

	var conventions []Convention
	conventions = append(conventions, detectInheritance(symbols, edges, symbolByID)...)
	conventions = append(conventions, detectNaming(symbols, files)...)
	conventions = append(conventions, detectStructure(symbols, files)...)
	conventions = append(conventions, detectComposition(symbols, edges, symbolByID)...)
	conventions = append(conventions, detectTesting(symbols, edges, files, symbolByID)...)

	sort.Slice(conventions, func(i, j int) bool {
		if conventions[i].Category != conventions[j].Category {
			return categoryOrder(conventions[i].Category) < categoryOrder(conventions[j].Category)
		}
		if conventions[i].Strength != conventions[j].Strength {
			return conventions[i].Strength > conventions[j].Strength
		}
		return conventions[i].Description < conventions[j].Description
	})

	if opts.MinStrength > 0 {
		filtered := conventions[:0]
		for _, c := range conventions {
			if c.Strength >= opts.MinStrength {
				filtered = append(filtered, c)
			}
		}
		conventions = filtered
	}

	return conventions, len(symbols), nil
}

type symbolRow struct {
	id       int64
	fileID   int64
	name     string
	qualified string
	kind     string
	parentID *int64
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
		var e edgeRow
		if err := rows.Scan(&e.sourceID, &e.targetID, &e.kind); err != nil {
			return nil, err
		}
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

func detectInheritance(symbols []symbolRow, edges []edgeRow, symbolByID map[int64]symbolRow) []Convention {

	// Group: for each target of an "inherits" edge, count sources of same kind.
	type inheritGroup struct {
		targetName string
		sourceKind string
		count      int
	}

	type groupKey struct {
		targetID   int64
		sourceKind string
	}
	groups := map[groupKey]*inheritGroup{}

	for _, e := range edges {
		if e.kind != "inherits" {
			continue
		}
		src, ok := symbolByID[e.sourceID]
		if !ok {
			continue
		}
		tgt, ok := symbolByID[e.targetID]
		if !ok {
			continue
		}
		key := groupKey{targetID: e.targetID, sourceKind: src.kind}
		if g, exists := groups[key]; exists {
			g.count++
		} else {
			groups[key] = &inheritGroup{
				targetName: tgt.name,
				sourceKind: src.kind,
				count:      1,
			}
		}
	}

	// Total: all symbols of that kind
	kindCounts := map[string]int{}
	for _, s := range symbols {
		kindCounts[s.kind]++
	}

	var out []Convention
	for _, g := range groups {
		if g.count < minInstances {
			continue
		}
		total := kindCounts[g.sourceKind]
		if total == 0 {
			continue
		}
		out = append(out, Convention{
			Category:    CategoryInheritance,
			Description: fmt.Sprintf("%d %s symbols inherit %s", g.count, g.sourceKind, g.targetName),
			Instances:   g.count,
			Total:       total,
			Strength:    float64(g.count) / float64(total),
		})
	}
	return out
}

func detectNaming(symbols []symbolRow, files []fileRow) []Convention {
	fileByID := map[int64]string{}
	for _, f := range files {
		fileByID[f.id] = f.path
	}

	// Group symbols by kind, detect suffix patterns in names
	type kindSuffix struct {
		kind   string
		suffix string
	}
	suffixCounts := map[kindSuffix]int{}
	kindCounts := map[string]int{}

	for _, s := range symbols {
		if s.parentID != nil {
			continue
		}
		kindCounts[s.kind]++

		suffix := extractSuffix(s.name)
		if suffix == "" {
			continue
		}
		suffixCounts[kindSuffix{kind: s.kind, suffix: suffix}]++
	}

	// Also detect file naming patterns per kind
	type kindFileSuffix struct {
		kind   string
		suffix string
	}
	fileSuffixCounts := map[kindFileSuffix]int{}
	kindFileCounts := map[string]int{}

	for _, s := range symbols {
		if s.parentID != nil {
			continue
		}
		fp, ok := fileByID[s.fileID]
		if !ok {
			continue
		}
		base := path.Base(fp)
		kindFileCounts[s.kind]++
		fileSuffix := extractFileSuffix(base)
		if fileSuffix == "" {
			continue
		}
		fileSuffixCounts[kindFileSuffix{kind: s.kind, suffix: fileSuffix}]++
	}

	var out []Convention

	for ks, count := range suffixCounts {
		if count < minInstances {
			continue
		}
		total := kindCounts[ks.kind]
		if total == 0 {
			continue
		}
		out = append(out, Convention{
			Category:    CategoryNaming,
			Description: fmt.Sprintf("%s symbols use *%s naming", ks.kind, ks.suffix),
			Instances:   count,
			Total:       total,
			Strength:    float64(count) / float64(total),
		})
	}

	for ks, count := range fileSuffixCounts {
		if count < minInstances {
			continue
		}
		total := kindFileCounts[ks.kind]
		if total == 0 {
			continue
		}
		out = append(out, Convention{
			Category:    CategoryNaming,
			Description: fmt.Sprintf("%s files use *%s naming", ks.kind, ks.suffix),
			Instances:   count,
			Total:       total,
			Strength:    float64(count) / float64(total),
		})
	}

	return out
}

func detectStructure(symbols []symbolRow, files []fileRow) []Convention {
	fileByID := map[int64]string{}
	for _, f := range files {
		fileByID[f.id] = f.path
	}

	// Group top-level symbols by kind + directory pattern
	type kindDir struct {
		kind string
		dir  string
	}
	dirCounts := map[kindDir]int{}
	kindCounts := map[string]int{}

	for _, s := range symbols {
		if s.parentID != nil {
			continue
		}
		fp, ok := fileByID[s.fileID]
		if !ok {
			continue
		}
		dir := path.Dir(fp)
		kindCounts[s.kind]++
		dirCounts[kindDir{kind: s.kind, dir: dir}]++
	}

	var out []Convention
	for kd, count := range dirCounts {
		if count < minInstances {
			continue
		}
		total := kindCounts[kd.kind]
		if total == 0 {
			continue
		}
		out = append(out, Convention{
			Category:    CategoryStructure,
			Description: fmt.Sprintf("%s symbols live in %s/", kd.kind, kd.dir),
			Instances:   count,
			Total:       total,
			Strength:    float64(count) / float64(total),
		})
	}
	return out
}

func detectComposition(symbols []symbolRow, edges []edgeRow, symbolByID map[int64]symbolRow) []Convention {

	type groupKey struct {
		targetID   int64
		sourceKind string
	}
	type compGroup struct {
		targetName string
		sourceKind string
		count      int
	}
	groups := map[groupKey]*compGroup{}

	for _, e := range edges {
		if e.kind != "includes" && e.kind != "composes" {
			continue
		}
		src, ok := symbolByID[e.sourceID]
		if !ok {
			continue
		}
		tgt, ok := symbolByID[e.targetID]
		if !ok {
			continue
		}
		key := groupKey{targetID: e.targetID, sourceKind: src.kind}
		if g, exists := groups[key]; exists {
			g.count++
		} else {
			groups[key] = &compGroup{
				targetName: tgt.name,
				sourceKind: src.kind,
				count:      1,
			}
		}
	}

	kindCounts := map[string]int{}
	for _, s := range symbols {
		kindCounts[s.kind]++
	}

	var out []Convention
	for _, g := range groups {
		if g.count < minInstances {
			continue
		}
		total := kindCounts[g.sourceKind]
		if total == 0 {
			continue
		}
		out = append(out, Convention{
			Category:    CategoryComposition,
			Description: fmt.Sprintf("%d %s symbols include %s", g.count, g.sourceKind, g.targetName),
			Instances:   g.count,
			Total:       total,
			Strength:    float64(g.count) / float64(total),
		})
	}
	return out
}

func detectTesting(symbols []symbolRow, edges []edgeRow, files []fileRow, symbolByID map[int64]symbolRow) []Convention {
	fileByID := map[int64]string{}
	for _, f := range files {
		fileByID[f.id] = f.path
	}

	// Detect test file naming conventions from files with "tests" edges
	testFileIDs := map[int64]struct{}{}
	for _, e := range edges {
		if e.kind != "tests" {
			continue
		}
		if src, ok := symbolByID[e.sourceID]; ok {
			testFileIDs[src.fileID] = struct{}{}
		}
	}

	// If no tests edges, infer from file naming patterns
	if len(testFileIDs) == 0 {
		for _, f := range files {
			base := path.Base(f.path)
			if strings.Contains(base, "_test") || strings.Contains(base, ".test.") || strings.HasPrefix(base, "test_") {
				testFileIDs[f.id] = struct{}{}
			}
		}
	}

	if len(testFileIDs) < minInstances {
		return nil
	}

	// Detect naming pattern among test files
	suffixes := map[string]int{}
	for fid := range testFileIDs {
		fp, ok := fileByID[fid]
		if !ok {
			continue
		}
		base := path.Base(fp)
		if idx := strings.LastIndex(base, "_test"); idx >= 0 {
			ext := base[idx:]
			suffixes[ext]++
		} else if idx := strings.LastIndex(base, ".test."); idx >= 0 {
			ext := base[idx:]
			suffixes[ext]++
		} else if strings.HasPrefix(base, "test_") {
			suffixes["test_*"]++
		}
	}

	total := len(testFileIDs)
	var out []Convention
	for suffix, count := range suffixes {
		if count < minInstances {
			continue
		}
		out = append(out, Convention{
			Category:    CategoryTesting,
			Description: fmt.Sprintf("%d/%d test files use *%s naming", count, total, suffix),
			Instances:   count,
			Total:       total,
			Strength:    float64(count) / float64(total),
		})
	}
	return out
}

func indexSymbols(symbols []symbolRow) map[int64]symbolRow {
	m := make(map[int64]symbolRow, len(symbols))
	for _, s := range symbols {
		m[s.id] = s
	}
	return m
}

func extractSuffix(name string) string {
	// CamelCase suffix: "CheckoutService" -> "Service"
	// Snake_case suffix: "checkout_service" -> "_service"
	if idx := strings.LastIndex(name, "_"); idx > 0 && idx < len(name)-1 {
		return name[idx:]
	}
	// CamelCase: find last uppercase letter that starts a word
	lastUpper := -1
	for i := len(name) - 1; i > 0; i-- {
		if name[i] >= 'A' && name[i] <= 'Z' {
			lastUpper = i
			break
		}
	}
	if lastUpper > 0 {
		return name[lastUpper:]
	}
	return ""
}

func categoryOrder(c Category) int {
	switch c {
	case CategoryInheritance:
		return 0
	case CategoryNaming:
		return 1
	case CategoryStructure:
		return 2
	case CategoryComposition:
		return 3
	case CategoryTesting:
		return 4
	}
	return 5
}

func extractFileSuffix(basename string) string {
	// "checkout_service.rb" -> "_service.rb"
	// "orders_controller.rb" -> "_controller.rb"
	ext := path.Ext(basename)
	nameOnly := strings.TrimSuffix(basename, ext)
	if idx := strings.LastIndex(nameOnly, "_"); idx > 0 {
		return nameOnly[idx:] + ext
	}
	return ""
}
