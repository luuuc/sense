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
const minInterfaceInstances = 2
const maxDescriptionNames = 5
const minPrevalence = 0.10
const minPrevalenceInstances = 5

type Category string

const (
	CategoryInheritance  Category = "inheritance"
	CategoryNaming       Category = "naming"
	CategoryStructure    Category = "structure"
	CategoryComposition  Category = "composition"
	CategoryTesting      Category = "testing"
	CategoryDesignPattern Category = "design_pattern"
	CategoryFramework    Category = "framework"
	CategoryArchitecture Category = "architecture"
)

type Example struct {
	Name      string
	Path      string
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

	enrichEdgeCounts(conventions, symbols, edges, filePathByID)

	sort.Slice(conventions, func(i, j int) bool {
		if conventions[i].Category != conventions[j].Category {
			return categoryOrder(conventions[i].Category) < categoryOrder(conventions[j].Category)
		}
		if conventions[i].Strength != conventions[j].Strength {
			return conventions[i].Strength > conventions[j].Strength
		}
		return conventions[i].Description < conventions[j].Description
	})

	{
		filtered := conventions[:0]
		for _, c := range conventions {
			prevalent := c.Instances >= minPrevalenceInstances ||
				(c.Total > 0 && float64(c.Instances)/float64(c.Total) >= minPrevalence)
			if prevalent {
				filtered = append(filtered, c)
			}
		}
		conventions = filtered
	}

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

func detectInheritance(symbols []symbolRow, edges []edgeRow, symbolByID map[int64]symbolRow, filePathByID map[int64]string) []Convention {

	type inheritGroup struct {
		targetName string
		sourceKind string
		count      int
		examples   []Example
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
			g.examples = append(g.examples, Example{Name: src.name, Path: filePathByID[src.fileID]})
		} else {
			groups[key] = &inheritGroup{
				targetName: tgt.name,
				sourceKind: src.kind,
				count:      1,
				examples:   []Example{{Name: src.name, Path: filePathByID[src.fileID]}},
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
		sortExamples(g.examples)
		out = append(out, Convention{
			Category:    CategoryInheritance,
			Description: fmt.Sprintf("%d %s extend %s as a base class (%s)", g.count, pluralize(g.sourceKind), g.targetName, topNames(g.examples)),
			Instances:   g.count,
			Total:       total,
			Strength:    float64(g.count) / float64(total),
			Examples:    g.examples,
		})
	}
	return out
}

func detectNaming(symbols []symbolRow, filePathByID map[int64]string) []Convention {
	type kindSuffix struct {
		kind   string
		suffix string
	}
	suffixCounts := map[kindSuffix]int{}
	suffixExamples := map[kindSuffix][]Example{}
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
		ks := kindSuffix{kind: s.kind, suffix: suffix}
		suffixCounts[ks]++
		suffixExamples[ks] = append(suffixExamples[ks], Example{Name: s.name, Path: filePathByID[s.fileID]})
	}

	type kindFileSuffix struct {
		kind   string
		suffix string
	}
	fileSuffixCounts := map[kindFileSuffix]int{}
	fileSuffixExamples := map[kindFileSuffix][]Example{}
	kindFileCounts := map[string]int{}

	for _, s := range symbols {
		if s.parentID != nil {
			continue
		}
		fp, ok := filePathByID[s.fileID]
		if !ok {
			continue
		}
		base := path.Base(fp)
		kindFileCounts[s.kind]++
		fileSuffix := extractFileSuffix(base)
		if fileSuffix == "" {
			continue
		}
		kfs := kindFileSuffix{kind: s.kind, suffix: fileSuffix}
		fileSuffixCounts[kfs]++
		fileSuffixExamples[kfs] = append(fileSuffixExamples[kfs], Example{Name: base, Path: fp})
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
		ex := suffixExamples[ks]
		sortExamples(ex)
		out = append(out, Convention{
			Category:    CategoryNaming,
			Description: fmt.Sprintf("%s use *%s naming convention (%s — %d of %d)", pluralize(ks.kind), ks.suffix, topNames(ex), count, total),
			Instances:   count,
			Total:       total,
			Strength:    float64(count) / float64(total),
			Examples:    ex,
		})
	}

	for kfs, count := range fileSuffixCounts {
		if count < minInstances {
			continue
		}
		total := kindFileCounts[kfs.kind]
		if total == 0 {
			continue
		}
		ex := fileSuffixExamples[kfs]
		sortExamples(ex)
		out = append(out, Convention{
			Category:    CategoryNaming,
			Description: fmt.Sprintf("%s files use *%s naming convention (%s — %d of %d)", kfs.kind, kfs.suffix, topNames(ex), count, total),
			Instances:   count,
			Total:       total,
			Strength:    float64(count) / float64(total),
			Examples:    ex,
		})
	}

	return out
}

func detectStructure(symbols []symbolRow, filePathByID map[int64]string) []Convention {
	type kindDir struct {
		kind string
		dir  string
	}
	dirCounts := map[kindDir]int{}
	dirExamples := map[kindDir][]Example{}
	kindCounts := map[string]int{}

	for _, s := range symbols {
		if s.parentID != nil {
			continue
		}
		fp, ok := filePathByID[s.fileID]
		if !ok {
			continue
		}
		dir := path.Dir(fp)
		kindCounts[s.kind]++
		kd := kindDir{kind: s.kind, dir: dir}
		dirCounts[kd]++
		dirExamples[kd] = append(dirExamples[kd], Example{Name: s.name, Path: fp})
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
		ex := dirExamples[kd]
		sortExamples(ex)
		out = append(out, Convention{
			Category:    CategoryStructure,
			Description: fmt.Sprintf("%s are grouped in %s/ (%s — %d of %d)", pluralize(kd.kind), kd.dir, topNames(ex), count, total),
			Instances:   count,
			Total:       total,
			Strength:    float64(count) / float64(total),
			Examples:    ex,
		})
	}
	return out
}

func detectComposition(symbols []symbolRow, edges []edgeRow, symbolByID map[int64]symbolRow, filePathByID map[int64]string) []Convention {

	type groupKey struct {
		targetID   int64
		sourceKind string
	}
	type compGroup struct {
		targetName string
		sourceKind string
		count      int
		examples   []Example
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
			g.examples = append(g.examples, Example{Name: src.name, Path: filePathByID[src.fileID]})
		} else {
			groups[key] = &compGroup{
				targetName: tgt.name,
				sourceKind: src.kind,
				count:      1,
				examples:   []Example{{Name: src.name, Path: filePathByID[src.fileID]}},
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
		sortExamples(g.examples)
		out = append(out, Convention{
			Category:    CategoryComposition,
			Description: fmt.Sprintf("%d %s mix in %s for shared behavior (%s)", g.count, pluralize(g.sourceKind), g.targetName, topNames(g.examples)),
			Instances:   g.count,
			Total:       total,
			Strength:    float64(g.count) / float64(total),
			Examples:    g.examples,
		})
	}
	return out
}

func detectTesting(symbols []symbolRow, edges []edgeRow, filePathByID map[int64]string, symbolByID map[int64]symbolRow) []Convention {
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
		for fid, fp := range filePathByID {
			base := path.Base(fp)
			if strings.Contains(base, "_test") || strings.Contains(base, ".test.") || strings.HasPrefix(base, "test_") {
				testFileIDs[fid] = struct{}{}
			}
		}
	}

	if len(testFileIDs) < minInstances {
		return nil
	}

	// Detect naming pattern among test files
	suffixes := map[string]int{}
	for fid := range testFileIDs {
		fp, ok := filePathByID[fid]
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
		var ex []Example
		for fid := range testFileIDs {
			fp, ok := filePathByID[fid]
			if !ok {
				continue
			}
			base := path.Base(fp)
			if strings.Contains(base, suffix) || (suffix == "test_*" && strings.HasPrefix(base, "test_")) {
				ex = append(ex, Example{Name: base, Path: fp})
			}
		}
		sortExamples(ex)
		out = append(out, Convention{
			Category:    CategoryTesting,
			Description: fmt.Sprintf("Test files use *%s naming convention (%s — %d of %d test files)", suffix, topNames(ex), count, total),
			Instances:   count,
			Total:       total,
			Strength:    float64(count) / float64(total),
			Examples:    ex,
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
	case CategoryDesignPattern:
		return 5
	case CategoryFramework:
		return 6
	case CategoryArchitecture:
		return 7
	}
	return 8
}

func hasMatchingExample(examples []Example, domain string) bool {
	for _, e := range examples {
		if strings.Contains(e.Path, domain) {
			return true
		}
	}
	return false
}

func sortExamples(examples []Example) {
	sort.Slice(examples, func(i, j int) bool {
		return examples[i].Path < examples[j].Path
	})
}

func PickRepresentatives(examples []Example, max int) []string {
	if len(examples) == 0 {
		return nil
	}
	sorted := make([]Example, len(examples))
	copy(sorted, examples)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].EdgeCount > sorted[j].EdgeCount
	})
	n := max
	if n > len(sorted) {
		n = len(sorted)
	}
	names := make([]string, n)
	for i := 0; i < n; i++ {
		names[i] = sorted[i].Name
	}
	return names
}

func topNames(examples []Example) string {
	return strings.Join(PickRepresentatives(examples, maxDescriptionNames), ", ")
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

var serviceEntryPoints = map[string]bool{
	"call": true, "execute": true, "perform": true, "run": true,
	"handle": true, "process": true, "invoke": true,
}

func detectDesignPatterns(symbols []symbolRow, symbolByID map[int64]symbolRow, filePathByID map[int64]string) []Convention {
	childrenByParent := map[int64][]symbolRow{}
	for _, s := range symbols {
		if s.parentID != nil {
			childrenByParent[*s.parentID] = append(childrenByParent[*s.parentID], s)
		}
	}

	var serviceObjects []Example
	for parentID, children := range childrenByParent {
		parent, ok := symbolByID[parentID]
		if !ok || (parent.kind != "class" && parent.kind != "struct") {
			continue
		}
		var methods []symbolRow
		for _, c := range children {
			if c.kind == "function" || c.kind == "method" {
				methods = append(methods, c)
			}
		}
		if len(methods) == 0 || len(methods) > 2 {
			continue
		}
		for _, m := range methods {
			if serviceEntryPoints[strings.ToLower(m.name)] {
				serviceObjects = append(serviceObjects, Example{
					Name: parent.name,
					Path: filePathByID[parent.fileID],
				})
				break
			}
		}
	}

	var out []Convention
	if len(serviceObjects) >= minInstances {
		sortExamples(serviceObjects)
		totalParents := countParents(symbols)
		out = append(out, Convention{
			Category:    CategoryDesignPattern,
			Description: fmt.Sprintf("Service object pattern: %s use a single entry-point method (call/execute/perform) — %d of %d classes", topNames(serviceObjects), len(serviceObjects), totalParents),
			Instances:   len(serviceObjects),
			Total:       totalParents,
			Strength:    safeStrength(len(serviceObjects), totalParents),
			Examples:    serviceObjects,
		})
	}
	return out
}

func detectFrameworkIdioms(symbols []symbolRow, edges []edgeRow, symbolByID map[int64]symbolRow, filePathByID map[int64]string) []Convention {
	var out []Convention

	// Rails concerns: modules in paths containing "concerns" included by multiple classes
	type concernGroup struct {
		module   symbolRow
		includers []Example
	}
	concerns := map[int64]*concernGroup{}
	for _, e := range edges {
		if e.kind != "includes" {
			continue
		}
		tgt, ok := symbolByID[e.targetID]
		if !ok || tgt.kind != "module" {
			continue
		}
		fp := filePathByID[tgt.fileID]
		if !strings.Contains(fp, "concerns") && !strings.Contains(fp, "concern") {
			continue
		}
		src, ok := symbolByID[e.sourceID]
		if !ok {
			continue
		}
		g, exists := concerns[e.targetID]
		if !exists {
			g = &concernGroup{module: tgt}
			concerns[e.targetID] = g
		}
		g.includers = append(g.includers, Example{Name: src.name, Path: filePathByID[src.fileID]})
	}
	var concernExamples []Example
	for _, g := range concerns {
		if len(g.includers) < minInstances {
			continue
		}
		concernExamples = append(concernExamples, Example{
			Name: g.module.name,
			Path: filePathByID[g.module.fileID],
		})
	}
	if len(concernExamples) >= 1 {
		sortExamples(concernExamples)
		totalModules := countByKind(symbols, "module")
		for _, g := range concerns {
			if len(g.includers) < minInstances {
				continue
			}
			sortExamples(g.includers)
			out = append(out, Convention{
				Category:    CategoryFramework,
				Description: fmt.Sprintf("Concern pattern: %s is mixed into %d classes (%s) for shared behavior", g.module.name, len(g.includers), topNames(g.includers)),
				Instances:   len(g.includers),
				Total:       totalModules,
				Strength:    safeStrength(len(g.includers), totalModules),
				Examples:    g.includers,
			})
		}
	}

	// Go interface satisfaction: interfaces implemented by multiple structs
	type ifaceGroup struct {
		iface       symbolRow
		implementors []Example
	}
	ifaces := map[int64]*ifaceGroup{}
	for _, e := range edges {
		if e.kind != "inherits" {
			continue
		}
		tgt, ok := symbolByID[e.targetID]
		if !ok || tgt.kind != "interface" {
			continue
		}
		src, ok := symbolByID[e.sourceID]
		if !ok || (src.kind != "struct" && src.kind != "class") {
			continue
		}
		g, exists := ifaces[e.targetID]
		if !exists {
			g = &ifaceGroup{iface: tgt}
			ifaces[e.targetID] = g
		}
		g.implementors = append(g.implementors, Example{Name: src.name, Path: filePathByID[src.fileID]})
	}
	for _, g := range ifaces {
		if len(g.implementors) < minInterfaceInstances {
			continue
		}
		sortExamples(g.implementors)
		totalStructs := countByKind(symbols, "struct", "class")
		out = append(out, Convention{
			Category:    CategoryFramework,
			Description: fmt.Sprintf("Interface contract: %s is satisfied by %d types (%s) — polymorphic dispatch point", g.iface.name, len(g.implementors), topNames(g.implementors)),
			Instances:   len(g.implementors),
			Total:       totalStructs,
			Strength:    safeStrength(len(g.implementors), totalStructs),
			Examples:    g.implementors,
			KeySymbol:   g.iface.name,
		})
	}

	// Hook patterns: use* functions (React/TypeScript only)
	var hooks []Example
	for _, s := range symbols {
		if s.kind != "function" {
			continue
		}
		fp := filePathByID[s.fileID]
		ext := path.Ext(fp)
		if ext != ".js" && ext != ".jsx" && ext != ".ts" && ext != ".tsx" {
			continue
		}
		if strings.HasPrefix(s.name, "use") && len(s.name) > 3 && s.name[3] >= 'A' && s.name[3] <= 'Z' {
			hooks = append(hooks, Example{Name: s.name, Path: filePathByID[s.fileID]})
		}
	}
	if len(hooks) >= minInstances {
		sortExamples(hooks)
		totalFuncs := countByKind(symbols, "function")
		out = append(out, Convention{
			Category:    CategoryFramework,
			Description: fmt.Sprintf("React hook pattern: %s — custom hooks encapsulate stateful logic (%d hooks)", topNames(hooks), len(hooks)),
			Instances:   len(hooks),
			Total:       totalFuncs,
			Strength:    safeStrength(len(hooks), totalFuncs),
			Examples:    hooks,
		})
	}

	return out
}

func detectArchitectureLayers(symbols []symbolRow, edges []edgeRow, symbolByID map[int64]symbolRow, filePathByID map[int64]string) []Convention {
	symbolDir := map[int64]string{}
	for _, s := range symbols {
		fp, ok := filePathByID[s.fileID]
		if !ok {
			continue
		}
		parts := strings.Split(fp, "/")
		if len(parts) >= 2 {
			symbolDir[s.id] = parts[len(parts)-2]
		}
	}

	type dirPair struct{ from, to string }
	callCounts := map[dirPair]int{}
	for _, e := range edges {
		if e.kind != "calls" {
			continue
		}
		fromDir := symbolDir[e.sourceID]
		toDir := symbolDir[e.targetID]
		if fromDir == "" || toDir == "" || fromDir == toDir {
			continue
		}
		callCounts[dirPair{fromDir, toDir}]++
	}

	totalCrossDirCalls := 0
	for _, count := range callCounts {
		totalCrossDirCalls += count
	}

	type layerEvidence struct {
		from, to string
		count    int
	}
	var oneWay []layerEvidence
	for pair, count := range callCounts {
		if count < minInstances {
			continue
		}
		reverse := callCounts[dirPair{pair.to, pair.from}]
		if reverse == 0 {
			oneWay = append(oneWay, layerEvidence{from: pair.from, to: pair.to, count: count})
		}
	}

	var out []Convention
	for _, le := range oneWay {
		var examples []Example
		for _, e := range edges {
			if e.kind != "calls" {
				continue
			}
			if symbolDir[e.sourceID] == le.from && symbolDir[e.targetID] == le.to {
				src := symbolByID[e.sourceID]
				examples = append(examples, Example{
					Name: src.name,
					Path: filePathByID[src.fileID],
				})
				if len(examples) >= 10 {
					break
				}
			}
		}
		sortExamples(examples)
		deduped := dedupeExamples(examples)
		out = append(out, Convention{
			Category:    CategoryArchitecture,
			Description: fmt.Sprintf("Layer boundary: %s/ depends on %s/ (%d calls, never reversed) — unidirectional dependency", le.from, le.to, le.count),
			Instances:   le.count,
			Total:       totalCrossDirCalls,
			Strength:    safeStrength(le.count, totalCrossDirCalls),
			Examples:    deduped,
		})
	}
	return out
}

func countParents(symbols []symbolRow) int {
	n := 0
	for _, s := range symbols {
		if s.parentID == nil && (s.kind == "class" || s.kind == "struct") {
			n++
		}
	}
	return n
}

func countByKind(symbols []symbolRow, kinds ...string) int {
	kindSet := map[string]bool{}
	for _, k := range kinds {
		kindSet[k] = true
	}
	n := 0
	for _, s := range symbols {
		if kindSet[s.kind] {
			n++
		}
	}
	return n
}

func safeStrength(instances, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(instances) / float64(total)
}

func pluralize(kind string) string {
	if strings.HasSuffix(kind, "ss") || strings.HasSuffix(kind, "ch") ||
		strings.HasSuffix(kind, "sh") || strings.HasSuffix(kind, "x") {
		return kind + "es"
	}
	if strings.HasSuffix(kind, "s") {
		return kind + "es"
	}
	return kind + "s"
}

func enrichEdgeCounts(conventions []Convention, symbols []symbolRow, edges []edgeRow, filePathByID map[int64]string) {
	type namePathKey struct{ name, path string }
	inbound := make(map[int64]int, len(symbols))
	for _, e := range edges {
		inbound[e.targetID]++
	}
	lookup := make(map[namePathKey]int, len(symbols))
	for _, s := range symbols {
		key := namePathKey{s.name, filePathByID[s.fileID]}
		if c := inbound[s.id]; c > lookup[key] {
			lookup[key] = c
		}
	}
	for ci := range conventions {
		for ei := range conventions[ci].Examples {
			ex := &conventions[ci].Examples[ei]
			ex.EdgeCount = lookup[namePathKey{ex.Name, ex.Path}]
		}
	}
}

func dedupeExamples(examples []Example) []Example {
	seen := map[string]bool{}
	var out []Example
	for _, e := range examples {
		key := e.Name + ":" + e.Path
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, e)
	}
	return out
}
