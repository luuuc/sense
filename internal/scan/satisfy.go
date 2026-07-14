package scan

import (
	"fmt"
	"sort"
	"strings"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/index"
	"github.com/luuuc/sense/internal/model"
)

// arity is the (params, results) slot count parsed from a method's
// declaration snippet. The zero value means the snippet didn't parse
// (multi-line declaration, truncation, unmodeled construct); name-only
// matching applies — UNKNOWN can only ever KEEP an edge, never drop one.
type arity struct {
	params, results int
	known           bool
}

// compatible reports whether two parsed arities allow satisfaction: either
// side UNKNOWN falls back to name-only matching; both known require equality.
func (a arity) compatible(b arity) bool {
	return !a.known || !b.known || a == b
}

type ifaceInfo struct {
	sym     model.Symbol
	methods map[string]arity
}

type structInfo struct {
	sym     model.Symbol
	methods map[string]arity
}

// satisfyInterfaces is a post-extraction pass that computes implicit
// interface satisfaction in Go. For each struct whose method set is a
// superset of an interface's method set, it emits an inherits edge at
// confidence 0.9.
//
// Runs after resolveAndWriteEdges so embedding (includes) edges are
// resolved and available for promoted-method computation. Scoped to
// Go files only — implicit interface satisfaction is a Go-specific
// semantic.
func (h *harness) satisfyInterfaces() error {
	goFiles, err := h.idx.FileIDsByLanguage(h.ctx, "go")
	if err != nil {
		return fmt.Errorf("satisfy: query go files: %w", err)
	}
	if len(goFiles) == 0 {
		return nil
	}

	syms, err := h.idx.Query(h.ctx, index.Filter{})
	if err != nil {
		return fmt.Errorf("satisfy: query symbols: %w", err)
	}
	if len(syms) == 0 {
		return nil
	}

	interfaces, structs := classifyGoSymbols(syms, goFiles)
	if len(interfaces) == 0 || len(structs) == 0 {
		return nil
	}

	collectMethodSets(syms, interfaces, structs)
	embeddings, err := h.loadEmbeddings()
	if err != nil {
		return err
	}
	// Interface sets expand first: struct-side promotion through an embedded
	// interface field unions the interface's already-expanded set.
	expandInterfaceMethodSets(interfaces, embeddings)
	promoteEmbeddedMethodSets(structs, interfaces, embeddings)

	// Buckets index the FINAL method sets — built any earlier, promoted
	// methods are missing and embedded satisfiers silently drop.
	written, err := h.writeSatisfactionEdges(interfaces, indexStructMethods(structs))
	if err != nil {
		return fmt.Errorf("satisfy: write edges: %w", err)
	}
	h.edges += written
	return nil
}

// classifyGoSymbols partitions the Go symbols into the interface and struct
// (class) sets satisfaction is computed over, keyed by symbol id.
func classifyGoSymbols(syms []model.Symbol, goFiles map[int64]bool) (map[int64]*ifaceInfo, map[int64]*structInfo) {
	interfaces := map[int64]*ifaceInfo{}
	structs := map[int64]*structInfo{}
	for i := range syms {
		s := &syms[i]
		if !goFiles[s.FileID] {
			continue
		}
		switch s.Kind {
		case model.KindInterface:
			interfaces[s.ID] = &ifaceInfo{
				sym:     *s,
				methods: map[string]arity{},
			}
		case model.KindClass:
			structs[s.ID] = &structInfo{
				sym:     *s,
				methods: map[string]arity{},
			}
		default:
		}
	}
	return interfaces, structs
}

// collectMethodSets fills each interface's method list and each struct's method
// set from the method symbols whose parent is in the respective map.
func collectMethodSets(syms []model.Symbol, interfaces map[int64]*ifaceInfo, structs map[int64]*structInfo) {
	for i := range syms {
		s := &syms[i]
		if s.Kind != model.KindMethod || s.ParentID == nil {
			continue
		}
		parentID := *s.ParentID
		if iface, ok := interfaces[parentID]; ok {
			iface.methods[s.Name] = parseMethodArity(s.Snippet)
		}
		if st, ok := structs[parentID]; ok {
			st.methods[s.Name] = parseMethodArity(s.Snippet)
		}
	}
}

// loadEmbeddings loads the resolved embedding (includes) edges once, as a
// source→targets adjacency shared by the interface-expansion and the
// struct-promotion passes.
func (h *harness) loadEmbeddings() (map[int64][]int64, error) {
	edges, err := h.idx.EdgesOfKind(h.ctx, model.EdgeIncludes)
	if err != nil {
		return nil, fmt.Errorf("satisfy: query embeddings: %w", err)
	}
	embeddings := map[int64][]int64{}
	for _, e := range edges {
		if e.SourceID == nil {
			continue
		}
		embeddings[*e.SourceID] = append(embeddings[*e.SourceID], e.TargetID)
	}
	return embeddings, nil
}

// expandInterfaceMethodSets closes every interface's method set over its
// embedded interfaces. Go makes embedded method sets fully transitive, so the
// expansion is a memoized full closure — a depth cap here would shrink
// REQUIRED sets and silently re-create false satisfaction on composites. The
// visiting guard terminates on cycles, which Go forbids but a mid-edit or
// misresolved index can still contain. Targets that are not known interfaces
// (stdlib, unresolved, structs) contribute nothing.
func expandInterfaceMethodSets(interfaces map[int64]*ifaceInfo, embeddings map[int64][]int64) {
	expanded := map[int64]bool{}
	for id := range interfaces {
		expandInterfaceMethods(id, interfaces, embeddings, expanded, map[int64]bool{})
	}
}

func expandInterfaceMethods(id int64, interfaces map[int64]*ifaceInfo, embeddings map[int64][]int64, expanded, visiting map[int64]bool) {
	if expanded[id] || visiting[id] {
		return
	}
	visiting[id] = true
	iface := interfaces[id]
	for _, embeddedID := range embeddings[id] {
		embedded, ok := interfaces[embeddedID]
		if !ok {
			continue
		}
		expandInterfaceMethods(embeddedID, interfaces, embeddings, expanded, visiting)
		for m, ar := range embedded.methods {
			if have, ok := iface.methods[m]; ok {
				// Same name from two sources: arity agreement keeps it;
				// disagreement collapses to UNKNOWN so map order never
				// decides which declaration wins.
				if have != ar {
					iface.methods[m] = arity{}
				}
				continue
			}
			iface.methods[m] = ar
		}
	}
	delete(visiting, id)
	expanded[id] = true
}

// promoteEmbeddedMethodSets promotes embedded structs' methods onto their
// embedders, up to depth 3, so an embedded method counts toward interface
// satisfaction. An embedded interface field delegates its whole (expanded)
// method set, so interface targets union in full.
func promoteEmbeddedMethodSets(structs map[int64]*structInfo, interfaces map[int64]*ifaceInfo, embeddings map[int64][]int64) {
	for id, st := range structs {
		promoteEmbeddedMethods(st, id, embeddings, structs, interfaces, 3)
	}
}

// indexStructMethods buckets the structs by method name so satisfaction can
// prune candidates instead of scanning every interface×struct pair. Each
// bucket is sorted by symbol ID: edge-write order stays deterministic.
func indexStructMethods(structs map[int64]*structInfo) map[string][]*structInfo {
	buckets := map[string][]*structInfo{}
	for _, st := range structs {
		for m := range st.methods {
			buckets[m] = append(buckets[m], st)
		}
	}
	for _, b := range buckets {
		sort.Slice(b, func(i, j int) bool { return b[i].sym.ID < b[j].sym.ID })
	}
	return buckets
}

// candidateStructs bounds the satisfiers of a required method set: a
// satisfying struct has every required method, so the smallest bucket contains
// them all. A required method with no bucket means no struct can satisfy —
// zero candidates, immediately.
func candidateStructs(required map[string]arity, buckets map[string][]*structInfo) []*structInfo {
	var smallest []*structInfo
	first := true
	for m := range required {
		b, ok := buckets[m]
		if !ok {
			return nil
		}
		if first || len(b) < len(smallest) {
			smallest, first = b, false
		}
	}
	return smallest
}

// writeSatisfactionEdges writes an inherits edge for every struct whose method
// set satisfies an interface's, in one transaction. Interfaces are visited in
// symbol-ID order and buckets are pre-sorted, so write order is deterministic.
func (h *harness) writeSatisfactionEdges(interfaces map[int64]*ifaceInfo, buckets map[string][]*structInfo) (int, error) {
	ordered := make([]*ifaceInfo, 0, len(interfaces))
	for _, iface := range interfaces {
		ordered = append(ordered, iface)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].sym.ID < ordered[j].sym.ID })

	var written int
	err := h.idx.InTx(h.ctx, func() error {
		for _, iface := range ordered {
			// Post-expansion an empty INDEXED set: interface{}, a
			// constraint-only interface, or a composite of purely
			// unresolvable (stdlib) embeds. Everything would satisfy it, so
			// emitting edges would be noise, not information.
			if len(iface.methods) == 0 {
				continue
			}
			for _, st := range candidateStructs(iface.methods, buckets) {
				if methodSetSatisfies(st.methods, iface.methods) {
					if werr := h.writeSatisfactionEdge(st.sym.ID, iface.sym.ID, st.sym.FileID); werr != nil {
						return werr
					}
					written++
				}
			}
		}
		return nil
	})
	return written, err
}

func promoteEmbeddedMethods(st *structInfo, id int64, embeddings map[int64][]int64, structs map[int64]*structInfo, interfaces map[int64]*ifaceInfo, depth int) {
	if depth <= 0 {
		return
	}
	for _, embeddedID := range embeddings[id] {
		if iface, ok := interfaces[embeddedID]; ok {
			// An embedded interface value delegates its whole method set;
			// the set is already fully expanded, no recursion needed.
			// Insert-if-absent: the embedder's own declaration shadows.
			for m, ar := range iface.methods {
				if _, ok := st.methods[m]; !ok {
					st.methods[m] = ar
				}
			}
			continue
		}
		embedded, ok := structs[embeddedID]
		if !ok {
			continue
		}
		promoteEmbeddedMethods(embedded, embeddedID, embeddings, structs, interfaces, depth-1)
		for m, ar := range embedded.methods {
			if _, ok := st.methods[m]; !ok {
				st.methods[m] = ar
			}
		}
	}
}

func methodSetSatisfies(methods, required map[string]arity) bool {
	for m, want := range required {
		have, ok := methods[m]
		if !ok {
			return false
		}
		if !have.compatible(want) {
			return false
		}
	}
	return true
}

func (h *harness) writeSatisfactionEdge(structID, ifaceID, fileID int64) error {
	_, err := h.idx.WriteEdge(h.ctx, &model.Edge{
		SourceID:   model.Int64Ptr(structID),
		TargetID:   ifaceID,
		Kind:       model.EdgeInherits,
		FileID:     fileID,
		Confidence: extract.ConfidenceConvention,
	})
	return err
}

// parseMethodArity derives the (params, results) slot counts from a method's
// stored declaration snippet. Both snippet shapes parse through one path:
// interface members (`Close() error`) and receiver forms
// (`func (r *T) Close() error {`) — the receiver group is skipped so the
// param group is always the one after the method name. The zero value
// (UNKNOWN) is returned for every construct the walker does not model:
// truncated multi-line declarations, cap-length snippets (a 200-byte cut can
// BALANCE into a wrong parse), backquoted struct tags, or missing groups.
// UNKNOWN can only ever keep an edge, never drop one.
func parseMethodArity(snippet string) arity {
	s := strings.TrimSpace(snippet)
	if s == "" || len(snippet) >= snippetMaxBytes || strings.ContainsRune(s, '`') {
		return arity{}
	}
	if strings.HasPrefix(s, "func ") {
		s = strings.TrimSpace(s[5:])
		if strings.HasPrefix(s, "(") { // receiver group
			_, end, _, ok := scanGroup(s, 0)
			if !ok {
				return arity{}
			}
			s = strings.TrimSpace(s[end:])
		}
	}
	open := strings.IndexByte(s, '(')
	if open <= 0 { // no param group, or no method name before it
		return arity{}
	}
	commas, end, content, ok := scanGroup(s, open)
	if !ok {
		return arity{}
	}
	params := 0
	if content {
		params = commas + 1
	}
	results, ok := countResults(s[end:])
	if !ok {
		return arity{}
	}
	return arity{params: params, results: results, known: true}
}

// scanGroup walks one balanced group starting at s[i] == '(' with a combined
// depth counter across (), [] and {} — commas inside brackets or braces never
// count as slot separators (generic receivers, Pair[K, V] params, inline
// structs). Returns the top-level comma count, the index just past the
// closing paren, whether the group has any content, and ok=false when the
// group never closes (a truncated multi-line declaration).
func scanGroup(s string, i int) (commas, end int, content, ok bool) {
	depth := 0
	for j := i; j < len(s); j++ {
		switch s[j] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
			if depth == 0 {
				return commas, j + 1, content, true
			}
		case ',':
			if depth == 1 {
				commas++
			}
		default:
			if depth >= 1 && !isSpaceByte(s[j]) {
				content = true
			}
		}
	}
	return 0, 0, false, false
}

// countResults classifies the text after the param group: nothing (or only
// the body's opening brace) → 0; a parenthesized group → its slot count; any
// bare type → 1 (a bare result can never contain a top-level comma — Go
// requires parentheses for tuples).
func countResults(rest string) (int, bool) {
	rest = strings.TrimSpace(rest)
	if cut := strings.IndexByte(rest, '{'); cut >= 0 && !strings.HasPrefix(rest, "(") && !strings.Contains(rest[:cut], "func(") && !strings.Contains(rest[:cut], "func (") {
		rest = strings.TrimSpace(rest[:cut])
	}
	if rest == "" {
		return 0, true
	}
	if rest[0] == '(' {
		commas, _, content, ok := scanGroup(rest, 0)
		if !ok {
			return 0, false
		}
		if !content {
			return 0, true
		}
		return commas + 1, true
	}
	return 1, true
}

func isSpaceByte(b byte) bool { return b == ' ' || b == '\t' }
