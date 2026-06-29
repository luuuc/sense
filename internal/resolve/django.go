package resolve

import (
	"strings"

	"github.com/luuuc/sense/internal/model"
)

// Django-specific resolution refinement. Kept in its own file (not baked into
// the generic resolver) so the framework convention lives beside the language it
// belongs to, mirroring extract/python/django.go and model/rails.go. The generic
// resolver only dispatches to the seams declared here.

// isDjangoModelModuleRef reports whether a symbol belongs to a Python Django
// "models" module, used by NewIndex to flag the source files where ORM
// relations are declared. Only Python files qualify.
func isDjangoModelModuleRef(r model.SymbolRef) bool {
	return r.Language == "python" && isDjangoModelPath(r.Path)
}

// isDjangoModelPath reports whether a file path is a Django "models" module: a
// `<app>/models.py` file or a `<app>/models/` package. Django ORM model classes
// live there by convention, so the candidate in a models module is the real
// referent of an ORM relation that collides with a same-named non-model symbol.
func isDjangoModelPath(path string) bool {
	if path == "models.py" || strings.HasPrefix(path, "models/") {
		return true
	}
	return strings.HasSuffix(path, "/models.py") || strings.Contains(path, "/models/")
}

// preferDjangoModelComposes reorders a same-named candidate set for a Django ORM
// `composes` edge so the symbols defined in a models module come first, when the
// edge originates from one. A Django FK/O2O/M2M target string like
// `"product.ProductVariant"` is emitted as the bare leaf `ProductVariant`
// (see extract/python/django.go), which collides with same-named GraphQL types
// and serializers. Those non-model symbols were winning the tie purely by lower
// scan id (pickBest's lowest-id fallback), wiring the dependency to the wrong
// node and hiding the reverse fan-out from blast/graph. Floating the
// models-module candidates ahead of the rest makes pickBest land on the model.
//
// It is purely a tie-break refinement: the set is unchanged in length, so
// pickBest still flags the collision ambiguous and clamps confidence exactly as
// before — only which same-named symbol wins changes. pickBest's same-file
// preference still takes precedence (order preserved within each partition), so
// a genuine same-file match is never overridden.
//
// The gate is deliberately narrow so it touches only Django ORM relations: it
// fires only for `composes` whose source file is itself a Python models module
// (where ORM fields are declared), never a generic type-annotation compose, and
// never another language's composes (Ruby `has_many`, Rust, TS). It is a no-op
// unless it actually discriminates — it returns the input untouched when no
// candidate is in a models module, or when every candidate already is.
func (ix *Index) preferDjangoModelComposes(matches []model.SymbolRef, req Request) []model.SymbolRef {
	if req.Kind != model.EdgeComposes || len(matches) < 2 {
		return matches
	}
	if ix.fileLang[req.SourceFileID] != "python" || !ix.fileModelModule[req.SourceFileID] {
		return matches
	}
	models := make([]model.SymbolRef, 0, len(matches))
	rest := make([]model.SymbolRef, 0, len(matches))
	for _, m := range matches {
		if m.Language == "python" && isDjangoModelPath(m.Path) {
			models = append(models, m)
		} else {
			rest = append(rest, m)
		}
	}
	if len(models) == 0 || len(rest) == 0 {
		return matches
	}
	// models was allocated with cap len(matches), but rest holds the remaining
	// elements, so this append cannot alias or overwrite the input slice.
	return append(models, rest...)
}
