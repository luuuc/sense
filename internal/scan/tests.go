package scan

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/index"
	"github.com/luuuc/sense/internal/model"
)

// associateTests derives `tests` edges by filename convention: a
// file whose name matches a known test pattern is paired with its
// implementation sibling in the same directory, and one `tests`
// edge lands per symbol in the implementation file, sourced from a
// representative symbol in the test file.
//
// The edges are written directly with concrete source/target ids
// (bypassing the resolver) because both ends are already known and
// routing through the qualified-name resolver would risk the
// scope-aware same-file preference picking a re-opened class in the
// test file over the intended implementation class.
//
// Same-directory pairing is handled by implSibling; cross-directory
// mirror trees (Rails: spec/models/user_spec.rb → app/models/user.rb)
// are handled by mirrorImpl. Other frameworks (Django, etc.) remain
// same-directory only until a real case demands it.
func (h *harness) associateTests() error {
	if len(h.indexedFiles) == 0 {
		return nil
	}

	idByPath, testsForImpl := buildTestPairs(h.indexedFiles)
	if len(testsForImpl) == 0 {
		return nil
	}

	// Stable iteration order for determinism (tests write under any
	// map hash seed).
	implPaths := make([]string, 0, len(testsForImpl))
	for p := range testsForImpl {
		implPaths = append(implPaths, p)
	}
	sort.Strings(implPaths)

	written, err := h.writeTestEdges(implPaths, idByPath, testsForImpl)
	if err != nil {
		return err
	}
	h.edges += written
	return nil
}

// buildTestPairs derives, from the indexed-file list, a path→id lookup and an
// implPath→test-file-ids map. The two pairings (same-directory siblings via
// implSibling, cross-directory mirror trees via mirrorImpl) are folded in one
// pass so a test file is paired with its implementation without re-querying
// sense_files.
func buildTestPairs(files []indexedFile) (map[string]int64, map[string][]int64) {
	idByPath := make(map[string]int64, len(files))
	testsForImpl := map[string][]int64{}
	for _, f := range files {
		idByPath[f.Path] = f.ID
		if implPath, ok := implSibling(f.Path, f.Language); ok {
			testsForImpl[implPath] = append(testsForImpl[implPath], f.ID)
		}
		for _, mp := range mirrorImpl(f.Path, f.Language) {
			testsForImpl[mp] = append(testsForImpl[mp], f.ID)
		}
	}
	return idByPath, testsForImpl
}

// writeTestEdges drains the impl→tests pairing into `tests` edges in one
// transaction, skipping impl paths that weren't indexed or have no symbols.
func (h *harness) writeTestEdges(implPaths []string, idByPath map[string]int64, testsForImpl map[string][]int64) (int, error) {
	var written int
	err := h.idx.InTx(h.ctx, func() error {
		for _, implPath := range implPaths {
			implFileID, ok := idByPath[implPath]
			if !ok {
				continue // impl file wasn't in the indexed set — skip.
			}
			implSymbols, err := h.idx.Query(h.ctx, index.Filter{FileID: implFileID})
			if err != nil {
				return fmt.Errorf("query impl symbols: %w", err)
			}
			if len(implSymbols) == 0 {
				continue
			}
			n, err := h.writeTestEdgesForImpl(implSymbols, testsForImpl[implPath])
			if err != nil {
				return err
			}
			written += n
		}
		return nil
	})
	return written, err
}

// writeTestEdgesForImpl writes one `tests` edge per implementation symbol,
// sourced from a representative symbol in each paired test file. Test file ids
// are sorted so the edge set is deterministic regardless of map order.
func (h *harness) writeTestEdgesForImpl(implSymbols []model.Symbol, testFileIDs []int64) (int, error) {
	sort.Slice(testFileIDs, func(i, j int) bool { return testFileIDs[i] < testFileIDs[j] })
	var written int
	for _, testFileID := range testFileIDs {
		testSymbols, err := h.idx.Query(h.ctx, index.Filter{FileID: testFileID})
		if err != nil {
			return written, fmt.Errorf("query test symbols: %w", err)
		}
		sourceID, ok := representativeTestSymbol(testSymbols)
		if !ok {
			continue // test file had no symbols (empty or parse-failed).
		}
		for _, implSym := range implSymbols {
			edge := &model.Edge{
				SourceID:   &sourceID,
				TargetID:   implSym.ID,
				Kind:       model.EdgeTests,
				FileID:     testFileID,
				Confidence: extract.ConfidenceTests,
			}
			if _, werr := h.idx.WriteEdge(h.ctx, edge); werr != nil {
				return written, fmt.Errorf("write tests edge: %w", werr)
			}
			written++
		}
	}
	return written, nil
}

// implSibling returns the expected implementation-file path for a
// test file, or (_, false) if the path doesn't match any known test
// convention. Matching is suffix / prefix -based and same-directory
// only; cross-directory mirror trees (Rails, Django) are not
// handled.
func implSibling(path, language string) (string, bool) {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	switch language {
	case "ruby":
		return siblingBySuffix(dir, base, []string{"_test.rb", "_spec.rb"}, ".rb")
	case "python":
		if strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py") {
			stem := strings.TrimSuffix(strings.TrimPrefix(base, "test_"), ".py")
			return filepath.Join(dir, stem+".py"), true
		}
	case "typescript":
		// TSX files arrive with language "tsx" (separate extractor
		// registration), so only .ts siblings belong here.
		return siblingBySuffix(dir, base, []string{".test.ts", ".spec.ts"}, ".ts")
	case "tsx":
		return siblingBySuffix(dir, base, []string{".test.tsx", ".spec.tsx"}, ".tsx")
	case "javascript":
		return jsImplSibling(dir, base)
	case "go":
		return siblingBySuffix(dir, base, []string{"_test.go"}, ".go")
	}
	return "", false
}

// siblingBySuffix returns the impl path for base when it ends with one of the
// test suffixes, swapping that suffix for implExt. Returns (_, false) when no
// suffix matches.
func siblingBySuffix(dir, base string, suffixes []string, implExt string) (string, bool) {
	for _, suffix := range suffixes {
		if strings.HasSuffix(base, suffix) {
			stem := strings.TrimSuffix(base, suffix)
			return filepath.Join(dir, stem+implExt), true
		}
	}
	return "", false
}

// jsImplSibling is the JavaScript case of implSibling: the implementation
// extension follows the test suffix (.jsx tests map to .jsx, the rest to .js),
// so it can't share siblingBySuffix's single fixed implExt.
func jsImplSibling(dir, base string) (string, bool) {
	for _, suffix := range []string{".test.js", ".spec.js", ".test.jsx", ".spec.jsx"} {
		if strings.HasSuffix(base, suffix) {
			stem := strings.TrimSuffix(base, suffix)
			ext := ".js"
			if strings.HasSuffix(suffix, ".jsx") {
				ext = ".jsx"
			}
			return filepath.Join(dir, stem+ext), true
		}
	}
	return "", false
}

// mirrorImpl handles cross-directory test conventions where test and
// implementation files live in parallel directory trees. Returns all
// candidate impl paths (caller checks which exist in the index).
//
// Supported conventions:
//   - Ruby/Rails: spec/models/user_spec.rb → app/models/user.rb
//   - Ruby/Rails: test/models/user_test.rb → app/models/user.rb
func mirrorImpl(path, language string) []string {
	if language != "ruby" {
		return nil
	}
	base := filepath.Base(path)
	var stem string
	for _, suffix := range []string{"_spec.rb", "_test.rb"} {
		if strings.HasSuffix(base, suffix) {
			stem = strings.TrimSuffix(base, suffix) + ".rb"
			break
		}
	}
	if stem == "" {
		return nil
	}

	// Normalise to forward-slash for prefix matching.
	norm := filepath.ToSlash(path)
	for _, prefix := range []string{"spec/", "test/"} {
		if !strings.HasPrefix(norm, prefix) {
			continue
		}
		rest := strings.TrimPrefix(norm, prefix)
		dir := filepath.Dir(rest)
		if dir == "." {
			dir = ""
		} else {
			dir += "/"
		}
		return []string{
			filepath.FromSlash("app/" + dir + stem),
		}
	}
	return nil
}

// representativeTestSymbol picks the topmost symbol (by line_start,
// then by id for tie-break) as the source of emitted `tests` edges.
// The blast engine's test-lookup query joins source_id → sense_files
// to surface the test file path; any symbol in the file will do, so
// the representative is a deterministic pick for stable output.
//
// The input slice is treated as read-only: we find the topmost entry
// with a single pass rather than sort-in-place, so callers holding
// onto the slice see it unchanged.
func representativeTestSymbol(symbols []model.Symbol) (int64, bool) {
	if len(symbols) == 0 {
		return 0, false
	}
	best := symbols[0]
	for _, s := range symbols[1:] {
		switch {
		case s.LineStart < best.LineStart:
			best = s
		case s.LineStart == best.LineStart && s.ID < best.ID:
			best = s
		}
	}
	return best.ID, true
}
