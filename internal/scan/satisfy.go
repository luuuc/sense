package scan

import (
	"fmt"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/index"
	"github.com/luuuc/sense/internal/model"
)

// stdlibInterface represents a well-known standard library interface
// whose method set is hardcoded so structs can satisfy it without
// indexing the standard library.
type stdlibInterface struct {
	qualified string
	methods   []string
}

// Unused today: stdlib symbols aren't in the index so there's no target_id to reference.
// Will produce edges once external symbol stubs are supported.
var stdlibInterfaces = []stdlibInterface{
	{"io.Reader", []string{"Read"}},
	{"io.Writer", []string{"Write"}},
	{"io.Closer", []string{"Close"}},
	{"io.ReadWriter", []string{"Read", "Write"}},
	{"io.ReadCloser", []string{"Read", "Close"}},
	{"io.WriteCloser", []string{"Write", "Close"}},
	{"fmt.Stringer", []string{"String"}},
	{"error", []string{"Error"}},
	{"sort.Interface", []string{"Len", "Less", "Swap"}},
	{"encoding.BinaryMarshaler", []string{"MarshalBinary"}},
	{"json.Marshaler", []string{"MarshalJSON"}},
	{"json.Unmarshaler", []string{"UnmarshalJSON"}},
}

type ifaceInfo struct {
	sym     model.Symbol
	methods []string
}

type structInfo struct {
	sym     model.Symbol
	methods map[string]bool
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

	interfaces := map[int64]*ifaceInfo{}
	structs := map[int64]*structInfo{}

	for i := range syms {
		s := &syms[i]
		if !goFiles[s.FileID] {
			continue
		}
		switch s.Kind {
		case model.KindInterface:
			interfaces[s.ID] = &ifaceInfo{sym: *s}
		case model.KindClass:
			structs[s.ID] = &structInfo{
				sym:     *s,
				methods: map[string]bool{},
			}
		}
	}

	if len(interfaces) == 0 || len(structs) == 0 {
		return nil
	}

	// Performance gate
	ifaceCount := int64(len(interfaces)) + int64(len(stdlibInterfaces))
	product := ifaceCount * int64(len(structs))
	if product > 500_000 {
		_, _ = fmt.Fprintf(h.warn, "satisfy: skipping (interfaces×structs = %d > 500K)\n", product)
		return nil
	}

	// Collect interface methods and struct methods from child symbols.
	for i := range syms {
		s := &syms[i]
		if s.Kind != model.KindMethod || s.ParentID == nil {
			continue
		}
		parentID := *s.ParentID
		if iface, ok := interfaces[parentID]; ok {
			iface.methods = append(iface.methods, s.Name)
		}
		if st, ok := structs[parentID]; ok {
			st.methods[s.Name] = true
		}
	}

	// Load resolved embedding edges and promote methods.
	edges, err := h.idx.EdgesOfKind(h.ctx, model.EdgeIncludes)
	if err != nil {
		return fmt.Errorf("satisfy: query embeddings: %w", err)
	}
	embeddings := map[int64][]int64{}
	for _, e := range edges {
		if e.SourceID == nil {
			continue
		}
		embeddings[*e.SourceID] = append(embeddings[*e.SourceID], e.TargetID)
	}
	for id, st := range structs {
		promoteEmbeddedMethods(st, id, embeddings, structs, 3)
	}

	// Check satisfaction against project interfaces.
	var written int
	err = h.idx.InTx(h.ctx, func() error {
		for _, iface := range interfaces {
			if len(iface.methods) == 0 {
				continue
			}
			for _, st := range structs {
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
	if err != nil {
		return fmt.Errorf("satisfy: write edges: %w", err)
	}
	h.edges += written
	return nil
}

func promoteEmbeddedMethods(st *structInfo, id int64, embeddings map[int64][]int64, structs map[int64]*structInfo, depth int) {
	if depth <= 0 {
		return
	}
	for _, embeddedID := range embeddings[id] {
		embedded, ok := structs[embeddedID]
		if !ok {
			continue
		}
		promoteEmbeddedMethods(embedded, embeddedID, embeddings, structs, depth-1)
		for m := range embedded.methods {
			st.methods[m] = true
		}
	}
}

func methodSetSatisfies(methods map[string]bool, required []string) bool {
	for _, m := range required {
		if !methods[m] {
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
