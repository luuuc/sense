package sqlite

import (
	"context"
	"fmt"
	"strings"
)

const contextBudget = 800

const reverseIncludesCap = 20

type symbolCtx struct {
	id         int64
	kind       string
	qualified  string
	parentName string
}

type symbolEdges struct {
	composes   []string
	includes   []string
	inherits   []string
	calls      []string
	includedBy []string
	defines    []string
}

// ContextForFile returns graph-derived context strings for all symbols
// in the given file. Each string is a natural-language description of
// the symbol's relationships, suitable for embedding input. The file
// path, symbol kind/name, and edge relationships are formatted as
// plain text and truncated by priority to fit the embedding budget.
func (a *Adapter) ContextForFile(ctx context.Context, fileID int64) (map[int64]string, error) {
	var filePath string
	err := a.db.QueryRowContext(ctx,
		`SELECT path FROM sense_files WHERE id = ?`, fileID,
	).Scan(&filePath)
	if err != nil {
		return nil, fmt.Errorf("sqlite ContextForFile path: %w", err)
	}

	syms, err := a.contextSymbols(ctx, fileID)
	if err != nil {
		return nil, err
	}
	if len(syms) == 0 {
		return nil, nil
	}

	edgeMap, err := a.contextEdges(ctx, fileID)
	if err != nil {
		return nil, err
	}

	hasModule := false
	for _, s := range syms {
		if s.kind == "module" {
			hasModule = true
			break
		}
	}
	reverseIncludes, err := a.contextReverseIncludes(ctx, fileID, hasModule)
	if err != nil {
		return nil, err
	}

	defines, err := a.contextDefines(ctx, fileID)
	if err != nil {
		return nil, err
	}

	result := make(map[int64]string, len(syms))
	for _, s := range syms {
		edges := edgeMap[s.id]
		edges.includedBy = reverseIncludes[s.id]
		edges.defines = defines[s.id]
		result[s.id] = formatSymbolContext(filePath, s, edges)
	}
	return result, nil
}

func (a *Adapter) contextSymbols(ctx context.Context, fileID int64) ([]symbolCtx, error) {
	const q = `SELECT s.id, s.kind, s.qualified, COALESCE(p.name, '')
		FROM sense_symbols s
		LEFT JOIN sense_symbols p ON s.parent_id = p.id
		WHERE s.file_id = ?
		ORDER BY s.id`

	rows, err := a.db.QueryContext(ctx, q, fileID)
	if err != nil {
		return nil, fmt.Errorf("sqlite ContextForFile symbols: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var syms []symbolCtx
	for rows.Next() {
		var s symbolCtx
		if err := rows.Scan(&s.id, &s.kind, &s.qualified, &s.parentName); err != nil {
			return nil, fmt.Errorf("sqlite ContextForFile symbols scan: %w", err)
		}
		syms = append(syms, s)
	}
	return syms, rows.Err()
}

func (a *Adapter) contextEdges(ctx context.Context, fileID int64) (map[int64]symbolEdges, error) {
	const q = `SELECT e.source_id, e.kind, t.name FROM sense_edges e
		JOIN sense_symbols t ON e.target_id = t.id
		WHERE e.source_id IN (SELECT id FROM sense_symbols WHERE file_id = ?)
		  AND e.kind IN ('composes','includes','inherits','calls')
		ORDER BY e.source_id, e.kind, t.name`

	rows, err := a.db.QueryContext(ctx, q, fileID)
	if err != nil {
		return nil, fmt.Errorf("sqlite ContextForFile edges: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := make(map[int64]symbolEdges)
	for rows.Next() {
		var sourceID int64
		var kind, name string
		if err := rows.Scan(&sourceID, &kind, &name); err != nil {
			return nil, fmt.Errorf("sqlite ContextForFile edges scan: %w", err)
		}
		e := result[sourceID]
		switch kind {
		case "composes":
			e.composes = append(e.composes, name)
		case "includes":
			e.includes = append(e.includes, name)
		case "inherits":
			e.inherits = append(e.inherits, name)
		case "calls":
			e.calls = append(e.calls, name)
		}
		result[sourceID] = e
	}
	return result, rows.Err()
}

func (a *Adapter) contextReverseIncludes(ctx context.Context, fileID int64, needed bool) (map[int64][]string, error) {
	if !needed {
		return nil, nil
	}

	const q = `SELECT e.target_id, s.name FROM sense_edges e
		JOIN sense_symbols s ON e.source_id = s.id
		WHERE e.target_id IN (SELECT id FROM sense_symbols WHERE file_id = ?)
		  AND e.kind = 'includes'
		ORDER BY e.target_id, s.name`

	rows, err := a.db.QueryContext(ctx, q, fileID)
	if err != nil {
		return nil, fmt.Errorf("sqlite ContextForFile reverse includes: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := make(map[int64][]string)
	for rows.Next() {
		var targetID int64
		var name string
		if err := rows.Scan(&targetID, &name); err != nil {
			return nil, fmt.Errorf("sqlite ContextForFile reverse includes scan: %w", err)
		}
		if len(result[targetID]) < reverseIncludesCap {
			result[targetID] = append(result[targetID], name)
		}
	}
	return result, rows.Err()
}

func (a *Adapter) contextDefines(ctx context.Context, fileID int64) (map[int64][]string, error) {
	const q = `SELECT parent_id, name FROM sense_symbols
		WHERE file_id = ? AND parent_id IS NOT NULL
		ORDER BY parent_id, name`

	rows, err := a.db.QueryContext(ctx, q, fileID)
	if err != nil {
		return nil, fmt.Errorf("sqlite ContextForFile defines: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := make(map[int64][]string)
	for rows.Next() {
		var parentID int64
		var name string
		if err := rows.Scan(&parentID, &name); err != nil {
			return nil, fmt.Errorf("sqlite ContextForFile defines scan: %w", err)
		}
		result[parentID] = append(result[parentID], name)
	}
	return result, rows.Err()
}

type labeledItems struct {
	label string
	items []string
}

// formatSymbolContext builds a natural-language context string for a
// symbol from its file path, identity, and graph relationships.
// Lines are added in priority order (highest first). When a line
// would exceed contextBudget, its items are truncated to fit. If
// even one item won't fit, remaining lines are dropped.
func formatSymbolContext(filePath string, sym symbolCtx, edges symbolEdges) string {
	var b strings.Builder
	fmt.Fprintf(&b, "File: %s\n", filePath)

	if sym.parentName != "" && (sym.kind == "class" || sym.kind == "type") {
		fmt.Fprintf(&b, "%s %s < %s", sym.kind, sym.qualified, sym.parentName)
	} else {
		fmt.Fprintf(&b, "%s %s", sym.kind, sym.qualified)
	}
	header := b.String()

	// Relationship lines in priority order (highest first).
	// Truncation removes from the bottom: defines, then calls,
	// then included by, then includes, then composes.
	var groups []labeledItems
	if len(edges.inherits) > 0 {
		groups = append(groups, labeledItems{"inherits", edges.inherits})
	}
	if len(edges.composes) > 0 {
		groups = append(groups, labeledItems{"composes", edges.composes})
	}
	if len(edges.includes) > 0 {
		groups = append(groups, labeledItems{"includes", edges.includes})
	}
	if len(edges.includedBy) > 0 {
		groups = append(groups, labeledItems{"included by", edges.includedBy})
	}
	if len(edges.calls) > 0 {
		groups = append(groups, labeledItems{"calls", edges.calls})
	}
	if len(edges.defines) > 0 {
		groups = append(groups, labeledItems{"defines", edges.defines})
	}

	out := header
	for _, g := range groups {
		line := "\n" + g.label + ": " + strings.Join(g.items, ", ")
		if len(out)+len(line) <= contextBudget {
			out += line
			continue
		}
		// Fit as many items as possible.
		prefix := "\n" + g.label + ": "
		fitted := out + prefix
		if len(fitted) > contextBudget {
			break
		}
		for i, item := range g.items {
			sep := ""
			if i > 0 {
				sep = ", "
			}
			if len(fitted)+len(sep)+len(item) > contextBudget {
				break
			}
			fitted += sep + item
		}
		if len(fitted) > len(out)+len(prefix) {
			out = fitted
		}
		break
	}
	return out
}
