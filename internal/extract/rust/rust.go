// Package rust extracts Tier-Basic symbols and intra-file edges from
// Rust source via tree-sitter-rust.
//
// Symbol kinds:
//   - struct_item               → KindClass
//   - enum_item                 → KindClass
//   - trait_item                → KindInterface (traits are Rust's
//                                 interface equivalent)
//   - type_item (`type X = Y`)  → KindType
//   - const_item / static_item  → KindConstant (both are package-level
//                                 bound values; Sense doesn't model
//                                 the const/static distinction)
//   - function_item at module   → KindFunction
//   - function_item in impl     → KindMethod with parent = impl type
//   - mod_item                  → KindModule
//
// Intra-file edges:
//   - `impl Trait for Type`     → inherits (Type → Trait) when Trait
//                                 is defined in the same file.
//                                 Cross-file resolution waits for 01-03.
//
// Qualified-name rules:
//   - Rust uses `::` for path separators throughout. Module-scoped
//     items carry the module chain: `inner::helper`.
//   - Impl methods qualify through the impl's type:
//       impl Money                 → methods qualified `Money::display`
//       impl Formatter for Money   → methods qualified `Money::format`
//     ParentQualified holds the type name so card-10 resolution hooks
//     these methods onto the struct/enum symbol in the same file.
//
// What Tier-Basic skips:
//   - visibility (pub / pub(crate) / private) — pitch leaves it for
//     Full-tier.
//   - trait method signatures as symbols (they're part of the trait's
//     shape, not standalone symbols).
//   - macro-generated symbols: `vec![]`, `println!()`, derive macros,
//     `#[tokio::main]`. Macro expansion is out of scope per pitch.
//   - lifetimes, type parameters, and where-clauses on names.
package rust

import (
	"slices"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/grammars"
	"github.com/luuuc/sense/internal/model"
)

// Extractor is the Rust implementation of extract.Extractor.
type Extractor struct{}

func (Extractor) Grammar() *sitter.Language { return grammars.Rust() }
func (Extractor) Language() string          { return "rust" }
func (Extractor) Extensions() []string      { return []string{".rs"} }
func (Extractor) Tier() extract.Tier        { return extract.TierBasic }

func (Extractor) Extract(tree *sitter.Tree, source []byte, _ string, emit extract.Emitter) error {
	w := &walker{source: source, emit: emit}
	return w.walk(tree.RootNode(), nil)
}

func init() { extract.Register(Extractor{}) }

// ---- walker ----

type walker struct {
	source []byte
	emit   extract.Emitter
}

// walk dispatches on node kind under the given module scope. Unlike
// the class-focused walkers (Python/Ruby), Rust's walker is simpler:
// no nested classes, no "push scope for body" logic — the only scope
// that matters for qualification is module nesting, plus the impl-block
// indirection for methods.
func (w *walker) walk(n *sitter.Node, scope []string) error {
	if n == nil {
		return nil
	}
	switch n.Kind() {
	case "source_file", "declaration_list":
		return w.walkChildren(n, scope)
	case "struct_item":
		return w.handleTypeDef(n, scope, model.KindClass)
	case "enum_item":
		return w.handleTypeDef(n, scope, model.KindClass)
	case "trait_item":
		return w.handleTypeDef(n, scope, model.KindInterface)
	case "type_item":
		return w.handleTypeDef(n, scope, model.KindType)
	case "const_item", "static_item":
		return w.handleConstItem(n, scope)
	case "function_item":
		return w.handleFunction(n, scope)
	case "impl_item":
		return w.handleImpl(n, scope)
	case "mod_item":
		return w.handleMod(n, scope)
	default:
		return nil
	}
}

func (w *walker) walkChildren(n *sitter.Node, scope []string) error {
	count := n.NamedChildCount()
	for i := uint(0); i < count; i++ {
		if err := w.walk(n.NamedChild(i), scope); err != nil {
			return err
		}
	}
	return nil
}

// handleTypeDef emits a struct / enum / trait / type-alias symbol.
// The four share a shape: a `name` field plus scope-based
// qualification; the kind is passed in by the caller.
func (w *walker) handleTypeDef(n *sitter.Node, scope []string, kind model.SymbolKind) error {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := extract.Text(nameNode, w.source)
	if name == "" {
		return nil
	}
	parent := strings.Join(scope, "::")
	qualified := name
	if parent != "" {
		qualified = parent + "::" + name
	}
	return w.emit.Symbol(extract.EmittedSymbol{
		Name:            name,
		Qualified:       qualified,
		Kind:            kind,
		ParentQualified: parent,
		LineStart:       extract.Line(n.StartPosition()),
		LineEnd:         extract.Line(n.EndPosition()),
	})
}

// handleConstItem emits const_item and static_item as KindConstant.
func (w *walker) handleConstItem(n *sitter.Node, scope []string) error {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := extract.Text(nameNode, w.source)
	if name == "" {
		return nil
	}
	parent := strings.Join(scope, "::")
	qualified := name
	if parent != "" {
		qualified = parent + "::" + name
	}
	return w.emit.Symbol(extract.EmittedSymbol{
		Name:            name,
		Qualified:       qualified,
		Kind:            model.KindConstant,
		ParentQualified: parent,
		LineStart:       extract.Line(n.StartPosition()),
		LineEnd:         extract.Line(n.EndPosition()),
	})
}

// handleFunction emits a top-level function. Methods are emitted via
// handleImpl — a function_item at module scope lacks an impl parent
// type, so ParentQualified ends up being the current module chain
// (often empty at crate root).
func (w *walker) handleFunction(n *sitter.Node, scope []string) error {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := extract.Text(nameNode, w.source)
	if name == "" {
		return nil
	}
	parent := strings.Join(scope, "::")
	qualified := name
	if parent != "" {
		qualified = parent + "::" + name
	}
	return w.emit.Symbol(extract.EmittedSymbol{
		Name:            name,
		Qualified:       qualified,
		Kind:            model.KindFunction,
		ParentQualified: parent,
		LineStart:       extract.Line(n.StartPosition()),
		LineEnd:         extract.Line(n.EndPosition()),
	})
}

// handleImpl walks an `impl Type { … }` or `impl Trait for Type { … }`
// block. Functions inside become methods qualified through Type; if a
// trait is present, an inherits edge (Type → Trait) captures the
// trait implementation.
func (w *walker) handleImpl(n *sitter.Node, scope []string) error {
	implType := n.ChildByFieldName("type")
	if implType == nil {
		return nil
	}
	typeName := unwrapTypeName(implType, w.source)
	if typeName == "" {
		return nil
	}
	parent := strings.Join(scope, "::")
	typeQualified := typeName
	if parent != "" {
		typeQualified = parent + "::" + typeName
	}

	// impl Trait for Type → emit inherits edge from Type to Trait.
	if traitNode := n.ChildByFieldName("trait"); traitNode != nil {
		traitName := unwrapTypeName(traitNode, w.source)
		if traitName != "" {
			line := extract.Line(traitNode.StartPosition())
			if err := w.emit.Edge(extract.EmittedEdge{
				SourceQualified: typeQualified,
				TargetQualified: traitName,
				Kind:            model.EdgeInherits,
				Line:            &line,
				Confidence:      1.0,
			}); err != nil {
				return err
			}
		}
	}

	// Methods: functions in the impl body become KindMethod with the
	// impl type as parent.
	body := n.ChildByFieldName("body")
	if body == nil {
		return nil
	}
	for i := uint(0); i < body.NamedChildCount(); i++ {
		child := body.NamedChild(i)
		if child == nil || child.Kind() != "function_item" {
			continue
		}
		nameNode := child.ChildByFieldName("name")
		if nameNode == nil {
			continue
		}
		name := extract.Text(nameNode, w.source)
		if name == "" {
			continue
		}
		if err := w.emit.Symbol(extract.EmittedSymbol{
			Name:            name,
			Qualified:       typeQualified + "::" + name,
			Kind:            model.KindMethod,
			ParentQualified: typeQualified,
			LineStart:       extract.Line(child.StartPosition()),
			LineEnd:         extract.Line(child.EndPosition()),
		}); err != nil {
			return err
		}
	}
	return nil
}

// handleMod emits a module symbol and recurses into its body with the
// module name pushed onto the scope. Rust modules are lexical
// namespaces — unlike impl blocks, they do nest qualification.
func (w *walker) handleMod(n *sitter.Node, scope []string) error {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := extract.Text(nameNode, w.source)
	if name == "" {
		return nil
	}
	parent := strings.Join(scope, "::")
	qualified := name
	if parent != "" {
		qualified = parent + "::" + name
	}
	if err := w.emit.Symbol(extract.EmittedSymbol{
		Name:            name,
		Qualified:       qualified,
		Kind:            model.KindModule,
		ParentQualified: parent,
		LineStart:       extract.Line(n.StartPosition()),
		LineEnd:         extract.Line(n.EndPosition()),
	}); err != nil {
		return err
	}
	if body := n.ChildByFieldName("body"); body != nil {
		return w.walkChildren(body, append(slices.Clone(scope), name))
	}
	return nil
}

// unwrapTypeName peels generic_type and reference_type wrappers off a
// Rust type expression to reach the base type_identifier. Scoped
// identifiers (`std::vec::Vec`) resolve to the trailing segment only —
// cross-crate / cross-module resolution is 01-03's job.
func unwrapTypeName(t *sitter.Node, source []byte) string {
	for t != nil {
		switch t.Kind() {
		case "type_identifier":
			return extract.Text(t, source)
		case "generic_type":
			inner := t.ChildByFieldName("type")
			if inner == nil {
				return ""
			}
			t = inner
		case "reference_type":
			inner := t.ChildByFieldName("type")
			if inner == nil {
				return ""
			}
			t = inner
		case "scoped_type_identifier":
			// `foo::Bar` — trailing segment is the `name` field.
			name := t.ChildByFieldName("name")
			if name == nil {
				return ""
			}
			return extract.Text(name, source)
		default:
			return ""
		}
	}
	return ""
}
