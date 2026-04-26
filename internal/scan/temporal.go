package scan

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/luuuc/sense/internal/index"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

const (
	gitTimeout   = 2 * time.Second
	minCoChanges = 3
	maxCommits   = 1000
	historySince = "6 months ago"
)

// extractTemporalCoupling derives temporal edges from git co-change history.
// No-op if git is absent or the git log exceeds gitTimeout.
func (h *harness) extractTemporalCoupling() error {
	ctx, cancel := context.WithTimeout(h.ctx, gitTimeout)
	defer cancel()

	if !gitPresent(ctx, h.root) {
		return nil
	}

	commits, err := parseGitLog(ctx, h.root)
	if err != nil {
		if ctx.Err() != nil {
			_, _ = fmt.Fprintf(h.warn, "warn: git log timed out, skipping temporal coupling\n")
		}
		return nil
	}

	indexedFiles, err := h.indexedFilePaths()
	if err != nil {
		return err
	}
	if len(indexedFiles) == 0 {
		return nil
	}

	pairs, fileCounts := countCoChanges(commits, indexedFiles)

	var significant []pairKey
	for pair, count := range pairs {
		if count < minCoChanges {
			continue
		}
		significant = append(significant, pair)
	}
	if len(significant) == 0 {
		return nil
	}
	sort.Slice(significant, func(i, j int) bool {
		if significant[i].a != significant[j].a {
			return significant[i].a < significant[j].a
		}
		return significant[i].b < significant[j].b
	})

	repSymbols, err := h.representativeSymbols(indexedFiles)
	if err != nil {
		return err
	}

	if err := h.clearTemporalEdges(); err != nil {
		return err
	}

	var written int
	err = h.idx.InTx(h.ctx, func() error {
		edgeStmt, serr := h.idx.PrepareEdgeStmt(h.ctx)
		if serr != nil {
			return serr
		}
		defer func() { _ = edgeStmt.Close() }()

		for _, pk := range significant {
			symA, okA := repSymbols[pk.a]
			symB, okB := repSymbols[pk.b]
			if !okA || !okB {
				continue
			}

			coCount := pairs[pk]
			maxChanges := fileCounts[pk.a]
			if fileCounts[pk.b] > maxChanges {
				maxChanges = fileCounts[pk.b]
			}
			strength := float64(coCount) / float64(maxChanges)

			for _, dir := range []struct{ src, tgt model.Symbol }{
				{symA, symB},
				{symB, symA},
			} {
				srcID := dir.src.ID
				coChanges := coCount
				// Line stores co-change count for temporal edges (not a source line number).
				edge := &model.Edge{
					SourceID:   &srcID,
					TargetID:   dir.tgt.ID,
					Kind:       model.EdgeTemporal,
					FileID:     dir.src.FileID,
					Line:       &coChanges,
					Confidence: strength,
				}
				if _, werr := sqlite.ExecEdgeStmt(h.ctx, edgeStmt, edge); werr != nil {
					return fmt.Errorf("write temporal edge: %w", werr)
				}
				written++
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("temporal coupling: %w", err)
	}

	if written > 0 {
		h.edges += written
	}
	return nil
}

// gitPresent checks whether the root is inside a git repository.
func gitPresent(ctx context.Context, root string) bool {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--git-dir")
	cmd.Dir = root
	return cmd.Run() == nil
}

// parseGitLog runs git log and returns a list of commits, each with its
// set of changed file paths (relative to the repo root).
func parseGitLog(ctx context.Context, root string) ([][]string, error) {
	cmd := exec.CommandContext(ctx, "git", "log",
		"--name-only",
		"--no-merges",
		"--pretty=format:%H",
		"--since="+historySince,
		fmt.Sprintf("--max-count=%d", maxCommits),
	)
	cmd.Dir = root

	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	return parseGitLogOutput(out), nil
}

// parseGitLogOutput parses the raw output of git log --name-only into
// a slice of commits, each being a slice of file paths.
func parseGitLogOutput(data []byte) [][]string {
	var commits [][]string
	var current []string
	inCommit := false

	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if len(line) == 40 && isHexString(line) {
			if inCommit && len(current) > 0 {
				commits = append(commits, current)
			}
			current = nil
			inCommit = true
			continue
		}
		if inCommit {
			current = append(current, line)
		}
	}
	if inCommit && len(current) > 0 {
		commits = append(commits, current)
	}
	return commits
}

func isHexString(s string) bool {
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

type pairKey struct {
	a, b string
}

func makePairKey(a, b string) pairKey {
	if a > b {
		a, b = b, a
	}
	return pairKey{a, b}
}

// countCoChanges counts how many commits each file pair co-appeared in.
// Only counts pairs where both files are indexed and in different directories.
// Also returns per-file total change counts.
func countCoChanges(commits [][]string, indexed map[string]bool) (map[pairKey]int, map[string]int) {
	pairs := map[pairKey]int{}
	fileCounts := map[string]int{}

	for _, files := range commits {
		var relevant []string
		for _, f := range files {
			if indexed[f] {
				relevant = append(relevant, f)
				fileCounts[f]++
			}
		}
		for i := 0; i < len(relevant); i++ {
			for j := i + 1; j < len(relevant); j++ {
				if filepath.Dir(relevant[i]) == filepath.Dir(relevant[j]) {
					continue
				}
				pairs[makePairKey(relevant[i], relevant[j])]++
			}
		}
	}
	return pairs, fileCounts
}

// indexedFilePaths returns the set of file paths currently in the index.
func (h *harness) indexedFilePaths() (map[string]bool, error) {
	paths, err := h.idx.FilePaths(h.ctx)
	if err != nil {
		return nil, fmt.Errorf("temporal coupling: list files: %w", err)
	}
	m := make(map[string]bool, len(paths))
	for _, p := range paths {
		m[p] = true
	}
	return m, nil
}

// representativeSymbols maps each file path to its "most important" symbol.
// Preference: first class/module-level symbol, then highest connectivity.
func (h *harness) representativeSymbols(indexed map[string]bool) (map[string]model.Symbol, error) {
	db := h.idx.DB()
	ctx := h.ctx

	connectivity := map[int64]int{}
	rows, err := db.QueryContext(ctx, `
		SELECT source_id, COUNT(*) FROM sense_edges
		WHERE source_id IS NOT NULL AND kind != 'temporal'
		GROUP BY source_id`)
	if err != nil {
		return nil, fmt.Errorf("temporal coupling: load outbound connectivity: %w", err)
	}
	for rows.Next() {
		var id int64
		var cnt int
		if err := rows.Scan(&id, &cnt); err != nil {
			_ = rows.Close()
			return nil, err
		}
		connectivity[id] += cnt
	}
	_ = rows.Close()

	rows, err = db.QueryContext(ctx, `
		SELECT target_id, COUNT(*) FROM sense_edges
		WHERE kind != 'temporal'
		GROUP BY target_id`)
	if err != nil {
		return nil, fmt.Errorf("temporal coupling: load inbound connectivity: %w", err)
	}
	for rows.Next() {
		var id int64
		var cnt int
		if err := rows.Scan(&id, &cnt); err != nil {
			_ = rows.Close()
			return nil, err
		}
		connectivity[id] += cnt
	}
	_ = rows.Close()

	result := map[string]model.Symbol{}

	fileIDs := map[string]int64{}
	frows, err := db.QueryContext(ctx, `SELECT id, path FROM sense_files`)
	if err != nil {
		return nil, fmt.Errorf("temporal coupling: load file IDs: %w", err)
	}
	for frows.Next() {
		var id int64
		var path string
		if err := frows.Scan(&id, &path); err != nil {
			_ = frows.Close()
			return nil, err
		}
		if indexed[path] {
			fileIDs[path] = id
		}
	}
	_ = frows.Close()

	for path, fid := range fileIDs {
		symbols, err := h.idx.Query(h.ctx, index.Filter{FileID: fid})
		if err != nil {
			return nil, fmt.Errorf("temporal coupling: query symbols for %s: %w", path, err)
		}
		if len(symbols) == 0 {
			continue
		}
		rep := pickRepresentative(symbols, connectivity)
		result[path] = rep
	}
	return result, nil
}

var classLevelKinds = map[model.SymbolKind]bool{
	"class": true, "module": true, "interface": true, "trait": true,
}

// pickRepresentative selects the best symbol to represent a file in
// temporal coupling edges. Prefers class/module-level symbols, then
// falls back to highest connectivity.
func pickRepresentative(symbols []model.Symbol, connectivity map[int64]int) model.Symbol {
	var bestClass *model.Symbol
	var bestClassConn int
	var bestAny *model.Symbol
	var bestAnyConn int

	for i := range symbols {
		s := &symbols[i]
		conn := connectivity[s.ID]
		if classLevelKinds[s.Kind] {
			if bestClass == nil || conn > bestClassConn || (conn == bestClassConn && s.ID < bestClass.ID) {
				bestClass = s
				bestClassConn = conn
			}
		}
		if bestAny == nil || conn > bestAnyConn || (conn == bestAnyConn && s.ID < bestAny.ID) {
			bestAny = s
			bestAnyConn = conn
		}
	}

	if bestClass != nil {
		return *bestClass
	}
	return *bestAny
}

// clearTemporalEdges removes all existing temporal edges before re-computing.
func (h *harness) clearTemporalEdges() error {
	_, err := h.idx.DB().ExecContext(h.ctx,
		`DELETE FROM sense_edges WHERE kind = 'temporal'`)
	if err != nil {
		return fmt.Errorf("temporal coupling: clear old edges: %w", err)
	}
	return nil
}
