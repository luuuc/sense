package scan

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/sqlite"
)

// parseOpts controls how parseFileCore reports warnings and obtains a
// tree-sitter parser. The two callers (parallel walkTree, sequential
// RunIncremental) supply different implementations.
type parseOpts struct {
	ctx           context.Context
	maxFileSizeKB int
	warnf         func(warningKind, string, ...any)
	parserFor     func(extract.Extractor) (*sitter.Parser, bool)
}

// parseFileCore is the shared parse+extract body. Returns nil for files
// that should be skipped (unknown language, read/parse/extract failure,
// or when skip returns true for the computed hash).
func parseFileCore(po parseOpts, path, rel string, skip func(hash string) bool) *fileResult {
	ext := strings.ToLower(filepath.Ext(path))
	ex := extract.ForExtension(ext)
	if ex == nil {
		return nil
	}

	if po.maxFileSizeKB > 0 {
		info, err := os.Stat(path)
		if err != nil {
			po.warnf(warnMetaError, "%s (%v)", rel, err)
			return nil
		}
		if info.Size() > int64(po.maxFileSizeKB)*1024 {
			po.warnf(warnFileTooLarge, "%s (%d KB > %d KB max)", rel, info.Size()/1024, po.maxFileSizeKB)
			return nil
		}
	}

	if po.ctx.Err() != nil {
		return nil
	}

	source, err := os.ReadFile(path)
	if err != nil {
		po.warnf(warnMetaError, "%s (%v)", rel, err)
		return nil
	}

	newHash := hashSource(source)
	if skip(newHash) {
		return nil
	}

	collected := &collector{}

	if raw, ok := ex.(extract.RawExtractor); ok {
		if err := safeExtractRaw(raw, source, rel, collected); err != nil {
			po.warnf(warnParseFailed, "%s (%v)", rel, err)
			return nil
		}
	} else {
		parser, owned := po.parserFor(ex)
		if parser == nil {
			return nil
		}
		if owned {
			defer parser.Close()
		}

		tree := parser.Parse(source, nil)
		if tree == nil {
			po.warnf(warnParseFailed, "%s (nil parse tree)", rel)
			return nil
		}
		defer tree.Close()

		if tree.RootNode().HasError() {
			po.warnf(warnParseFailed, "%s (parse errors, best-effort extraction)", rel)
		}

		if err := safeExtract(ex, tree, source, rel, collected); err != nil {
			po.warnf(warnParseFailed, "%s (%v)", rel, err)
			return nil
		}
	}

	sort.SliceStable(collected.symbols, func(i, j int) bool {
		return len(collected.symbols[i].Qualified) < len(collected.symbols[j].Qualified)
	})

	return &fileResult{
		Rel:               rel,
		Language:          ex.Language(),
		Source:            source,
		Hash:              newHash,
		Symbols:           collected.symbols,
		Edges:             collected.edges,
		DispatchNames:     collected.dispatchNames,
		MentionedNames:    collected.mentionedNames,
		CgoExports:        collected.cgoExports,
		RustExports:       collected.rustExports,
		RustTestSymbols:   collected.rustTestSymbols,
		RustTraitMethods:  collected.rustTraitMethods,
		RustAllowDead:     collected.rustAllowDead,
		TSDecorated:       collected.tsDecorated,
		TSDefaultExports:  collected.tsDefaultExports,
		PyDecorated:       collected.pyDecorated,
		PyRoutes:          collected.pyRoutes,
		PyDjango:          collected.pyDjango,
		PyAllExports:      collected.pyAllExports,
		LangspecAnnotated: collected.lsAnnotated,
	}
}

// parseFileStandalone is the goroutine-safe parse function used by the
// parallel walkTree. It creates a fresh parser per call (no shared state).
func parseFileStandalone(
	ctx context.Context,
	path, rel string,
	hashMap map[string]sqlite.CachedFile,
	maxFileSizeKB int,
	wc *warningCollector,
	prog *progress,
) *fileResult {
	wf := func(kind warningKind, format string, args ...any) {
		wc.add(kind, fmt.Sprintf(format, args...))
		prog.incWarnings()
	}
	po := parseOpts{
		ctx:           ctx,
		maxFileSizeKB: maxFileSizeKB,
		warnf:         wf,
		parserFor: func(ex extract.Extractor) (*sitter.Parser, bool) {
			p := sitter.NewParser()
			if err := p.SetLanguage(ex.Grammar()); err != nil {
				p.Close()
				wf(warnParseFailed, "%s (%v)", rel, err)
				return nil, false
			}
			return p, true // caller owns — parseFileCore will defer Close
		},
	}
	return parseFileCore(po, path, rel, func(hash string) bool {
		cached, ok := hashMap[rel]
		return ok && cached.Hash == hash
	})
}

// parseFile is the sequential parse function used by RunIncremental.
// It uses the harness's cached parsers and per-file DB lookups.
func (h *harness) parseFile(path, rel string) *fileResult {
	po := parseOpts{
		ctx:           h.ctx,
		maxFileSizeKB: h.maxFileSizeKB,
		warnf:         h.addWarning,
		parserFor: func(ex extract.Extractor) (*sitter.Parser, bool) {
			p, err := h.parserFor(ex)
			if err != nil {
				h.addWarning(warnParseFailed, "%s (%v)", rel, err)
				return nil, false
			}
			return p, false // harness owns — do not close
		},
	}

	ext := strings.ToLower(filepath.Ext(path))
	ex := extract.ForExtension(ext)

	return parseFileCore(po, path, rel, func(hash string) bool {
		fileID, oldHash, metaErr := h.idx.FileMeta(h.ctx, rel)
		if metaErr != nil {
			h.addWarning(warnMetaError, "%s (%v)", rel, metaErr)
			return false
		}
		if oldHash != hash {
			return false
		}
		h.skipped++
		h.indexed++
		if fileID > 0 && ex != nil {
			h.indexedFiles = append(h.indexedFiles, indexedFile{ID: fileID, Path: rel, Language: ex.Language()})
		}
		return true
	})
}

// parserFor returns a cached parser for the extractor's language. The
// parser keeps its SetLanguage binding across calls — subsequent files
// in the same language reuse it without re-binding.
func (h *harness) parserFor(ex extract.Extractor) (*sitter.Parser, error) {
	if p, ok := h.parsers[ex.Language()]; ok {
		return p, nil
	}
	p := sitter.NewParser()
	if err := p.SetLanguage(ex.Grammar()); err != nil {
		p.Close()
		return nil, err
	}
	h.parsers[ex.Language()] = p
	return p, nil
}

// safeExtract wraps ex.Extract in a recover() so a bad extractor
// panicking on a weird CST node fails just this file, not the scan.
// Same posture as the fixture harness.
func safeExtract(ex extract.Extractor, tree *sitter.Tree, source []byte, rel string, c *collector) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("extractor panicked: %v", r)
		}
	}()
	return ex.Extract(tree, source, rel, c)
}

func safeExtractRaw(ex extract.RawExtractor, source []byte, rel string, c *collector) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("extractor panicked: %v", r)
		}
	}()
	return ex.ExtractRaw(source, rel, c)
}
