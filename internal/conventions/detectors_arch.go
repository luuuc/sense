package conventions

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

var serviceEntryPoints = map[string]bool{
	"call": true, "execute": true, "perform": true, "run": true,
	"handle": true, "process": true, "invoke": true,
}

func detectDesignPatterns(symbols []symbolRow, symbolByID map[int64]symbolRow, filePathByID map[int64]string) []Convention {
	serviceObjects := findServiceObjects(symbols, symbolByID, filePathByID)

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

// findServiceObjects returns one Example per class/struct that holds one or two
// methods, at least one of which is a service entry point (call/execute/…), the
// service-object shape.
func findServiceObjects(symbols []symbolRow, symbolByID map[int64]symbolRow, filePathByID map[int64]string) []Example {
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
	return serviceObjects
}

type dirPair struct{ from, to string }

type layerEvidence struct {
	from, to string
	count    int
}

func detectArchitectureLayers(symbols []symbolRow, edges []edgeRow, symbolByID map[int64]symbolRow, filePathByID map[int64]string) []Convention {
	symbolDir := mapSymbolsToLayers(symbols, filePathByID)
	callCounts, totalCrossDirCalls := countCrossLayerCalls(edges, symbolDir)
	oneWay := findUnidirectionalBoundaries(callCounts)

	var out []Convention
	for _, le := range oneWay {
		examples := gatherBoundaryExamples(edges, symbolDir, symbolByID, filePathByID, le.from, le.to)
		out = append(out, Convention{
			Category:    CategoryArchitecture,
			Description: fmt.Sprintf("Layer boundary: %s/ depends on %s/ (%d calls, never reversed) — unidirectional dependency", le.from, le.to, le.count),
			Instances:   le.count,
			Total:       totalCrossDirCalls,
			Strength:    safeStrength(le.count, totalCrossDirCalls),
			Examples:    examples,
		})
	}
	return out
}

// mapSymbolsToLayers assigns each symbol the module-level directory of its file.
func mapSymbolsToLayers(symbols []symbolRow, filePathByID map[int64]string) map[int64]string {
	symbolDir := map[int64]string{}
	for _, s := range symbols {
		fp, ok := filePathByID[s.fileID]
		if !ok {
			continue
		}
		symbolDir[s.id] = layerName(fp)
	}
	return symbolDir
}

// countCrossLayerCalls tallies calls that cross a layer boundary, returning the
// per-direction counts and the grand total of cross-layer calls.
func countCrossLayerCalls(edges []edgeRow, symbolDir map[int64]string) (map[dirPair]int, int) {
	callCounts := map[dirPair]int{}
	total := 0
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
		total++
	}
	return callCounts, total
}

// findUnidirectionalBoundaries returns the layer pairs whose calls are above
// threshold and never reversed, evidence of a one-way dependency.
func findUnidirectionalBoundaries(callCounts map[dirPair]int) []layerEvidence {
	var oneWay []layerEvidence
	for pair, count := range callCounts {
		if count < minInstances {
			continue
		}
		if callCounts[dirPair{pair.to, pair.from}] == 0 {
			oneWay = append(oneWay, layerEvidence{from: pair.from, to: pair.to, count: count})
		}
	}
	return oneWay
}

// gatherBoundaryExamples collects up to ten distinct calling symbols for a
// from→to layer boundary.
func gatherBoundaryExamples(edges []edgeRow, symbolDir map[int64]string, symbolByID map[int64]symbolRow, filePathByID map[int64]string, from, to string) []Example {
	var examples []Example
	for _, e := range edges {
		if e.kind != "calls" {
			continue
		}
		if symbolDir[e.sourceID] == from && symbolDir[e.targetID] == to {
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
	return dedupeExamples(examples)
}

// layerName returns the module-level directory for architecture layer detection.
// For deep trees (4+ components, e.g. Maven multi-module), uses the first two
// path components to avoid 75+ leaf-package boundaries. Provisional heuristic.
func layerName(fp string) string {
	parts := strings.Split(fp, "/")
	if len(parts) < 2 {
		return ""
	}
	if len(parts) >= 4 {
		return parts[0] + "/" + parts[1]
	}
	return parts[len(parts)-2]
}

func detectExternalDependencies(ctx context.Context, db *sql.DB, domain string, fileFilter []int64) []Convention {
	if len(fileFilter) == 0 && domain == "" {
		return nil
	}
	domainPattern := "%" + domain + "%"
	q := `SELECT t.qualified, COUNT(DISTINCT e.source_id) AS ref_count
	      FROM sense_edges e
	      JOIN sense_symbols t ON t.id = e.target_id
	      JOIN sense_files tf ON tf.id = t.file_id
	      JOIN sense_symbols s ON s.id = e.source_id
	      JOIN sense_files sf ON sf.id = s.file_id
	      WHERE e.kind IN ('calls', 'composes', 'inherits', 'includes')
	      AND sf.path LIKE ?
	      AND NOT tf.path LIKE ?
	      GROUP BY t.qualified
	      HAVING ref_count >= 3
	      ORDER BY ref_count DESC
	      LIMIT 5`
	rows, err := db.QueryContext(ctx, q, domainPattern, domainPattern)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()

	var out []Convention
	for rows.Next() {
		var qualified string
		var refCount int
		if err := rows.Scan(&qualified, &refCount); err != nil {
			continue
		}
		out = append(out, Convention{
			Category:    "external",
			Description: fmt.Sprintf("External integration: %s referenced by %d symbols", qualified, refCount),
			Instances:   refCount,
			Total:       refCount,
			Strength:    0.8,
			Examples:    []Example{{Name: qualified}},
		})
	}
	return out
}
