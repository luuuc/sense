// Package golang extracts Tier-Basic symbols and intra-file edges from
// Go source via tree-sitter-go. Directory is named `golang` because
// `package go` is illegal (Go reserved keyword); the exported
// Extractor type stays idiomatic.
//
// Qualification includes the package name (per 05-languages.md's
// `pkg.Type.Method` convention): Go's package boundary is always
// visible in a single file (the `package` clause), and every symbol's
// fully-qualified name in the Go ecosystem is package-prefixed. Other
// languages leave the package implicit because a single file doesn't
// always declare one (Python modules, JS files with no export, etc.).
//
// Symbol kinds emitted:
//   - struct type           → KindClass
//   - interface type        → KindInterface
//   - type alias / named    → KindType
//   - func at package scope → KindFunction  (no receiver)
//   - func with receiver    → KindMethod
//   - const NAME = …        → KindConstant  (any case; Go has no
//                             all-caps convention)
//
// What Tier-Basic skips:
//   - vars (package-level `var` bindings) — pitch explicitly scopes
//     to "constants". Full-tier can revisit.
//   - fields inside structs and interface method signatures.
//   - embedded types → composes edges (01-03 territory when the
//     full graph is needed).
//   - imports (01-03).
package golang

import (
	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/grammars"
	"github.com/luuuc/sense/internal/model"
)

// Extractor is the Go implementation of extract.Extractor.
type Extractor struct{}

func (Extractor) Grammar() *sitter.Language { return grammars.Go() }
func (Extractor) Language() string          { return "go" }
func (Extractor) Extensions() []string      { return []string{".go"} }
func (Extractor) Tier() extract.Tier        { return extract.TierBasic }

func (Extractor) Extract(tree *sitter.Tree, source []byte, _ string, emit extract.Emitter) error {
	w := &walker{
		source: source,
		emit:   emit,
		pkg:    packageName(tree.RootNode(), source),
	}
	return w.walkTopLevel(tree.RootNode())
}

func init() { extract.Register(Extractor{}) }

// ---- walker ----

type walker struct {
	source []byte
	emit   extract.Emitter
	pkg    string // package name, used to prefix every qualified name
}

// walkTopLevel iterates the direct named children of source_file and
// dispatches by kind. Go's symbol surface is flat (no nested classes),
// so we never descend beyond the declarations that sit at package
// scope. Function/method bodies are not walked — nested types and
// closures inside them are not Tier-Basic symbols.
func (w *walker) walkTopLevel(root *sitter.Node) error {
	if root == nil {
		return nil
	}
	count := root.NamedChildCount()
	for i := uint(0); i < count; i++ {
		n := root.NamedChild(i)
		if n == nil {
			continue
		}
		var err error
		switch n.Kind() {
		case "const_declaration":
			err = w.handleConstDeclaration(n)
		case "type_declaration":
			err = w.handleTypeDeclaration(n)
		case "function_declaration":
			err = w.handleFunction(n)
		case "method_declaration":
			err = w.handleMethod(n)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// handleConstDeclaration walks const_spec children. Both the grouped
// form (`const ( A = 1; B = 2 )`) and the single form (`const A = 1`)
// produce const_spec children.
func (w *walker) handleConstDeclaration(n *sitter.Node) error {
	for i := uint(0); i < n.NamedChildCount(); i++ {
		spec := n.NamedChild(i)
		if spec == nil || spec.Kind() != "const_spec" {
			continue
		}
		if err := w.emitConstSpec(spec); err != nil {
			return err
		}
	}
	return nil
}

// emitConstSpec emits one Symbol per name in a const_spec. Specs like
// `const A, B = 1, 2` declare multiple names simultaneously — each
// becomes its own symbol.
func (w *walker) emitConstSpec(spec *sitter.Node) error {
	// `name` is a repeated field on const_spec; ChildrenByFieldName
	// needs a cursor, so iterate manually by field-name match.
	for i := uint(0); i < spec.NamedChildCount(); i++ {
		c := spec.NamedChild(i)
		if c == nil || c.Kind() != "identifier" {
			continue
		}
		name := extract.Text(c, w.source)
		if name == "" {
			continue
		}
		if err := w.emit.Symbol(extract.EmittedSymbol{
			Name:      name,
			Qualified: w.qualify(name),
			Kind:      model.KindConstant,
			LineStart: extract.Line(spec.StartPosition()),
			LineEnd:   extract.Line(spec.EndPosition()),
		}); err != nil {
			return err
		}
	}
	return nil
}

// handleTypeDeclaration walks type_spec / type_alias children. Both
// forms classify by the inner `type` field's kind.
func (w *walker) handleTypeDeclaration(n *sitter.Node) error {
	for i := uint(0); i < n.NamedChildCount(); i++ {
		spec := n.NamedChild(i)
		if spec == nil {
			continue
		}
		switch spec.Kind() {
		case "type_spec":
			if err := w.emitTypeSpec(spec, false); err != nil {
				return err
			}
		case "type_alias":
			if err := w.emitTypeSpec(spec, true); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *walker) emitTypeSpec(spec *sitter.Node, isAlias bool) error {
	nameNode := spec.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := extract.Text(nameNode, w.source)
	if name == "" {
		return nil
	}

	kind := model.KindType
	if !isAlias {
		// `type X struct {}` → class, `type X interface {}` → interface,
		// anything else (`type X []int`, `type X Other`) → KindType.
		if t := spec.ChildByFieldName("type"); t != nil {
			switch t.Kind() {
			case "struct_type":
				kind = model.KindClass
			case "interface_type":
				kind = model.KindInterface
			}
		}
	}

	return w.emit.Symbol(extract.EmittedSymbol{
		Name:      name,
		Qualified: w.qualify(name),
		Kind:      kind,
		LineStart: extract.Line(spec.StartPosition()),
		LineEnd:   extract.Line(spec.EndPosition()),
	})
}

// handleFunction emits a top-level function (no receiver).
func (w *walker) handleFunction(n *sitter.Node) error {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := extract.Text(nameNode, w.source)
	if name == "" {
		return nil
	}
	return w.emit.Symbol(extract.EmittedSymbol{
		Name:      name,
		Qualified: w.qualify(name),
		Kind:      model.KindFunction,
		LineStart: extract.Line(n.StartPosition()),
		LineEnd:   extract.Line(n.EndPosition()),
	})
}

// handleMethod emits a method with receiver-type qualification. The
// receiver syntax is `func (r ReceiverType) Name(...)` or
// `func (r *ReceiverType) Name(...)`; we strip the pointer and any
// type parameters to get the base name used for intra-file resolution.
func (w *walker) handleMethod(n *sitter.Node) error {
	nameNode := n.ChildByFieldName("name")
	receiver := n.ChildByFieldName("receiver")
	if nameNode == nil || receiver == nil {
		return nil
	}
	name := extract.Text(nameNode, w.source)
	if name == "" {
		return nil
	}
	recvName := receiverType(receiver, w.source)
	if recvName == "" {
		return nil
	}
	parent := w.qualify(recvName)
	return w.emit.Symbol(extract.EmittedSymbol{
		Name:            name,
		Qualified:       parent + "." + name,
		Kind:            model.KindMethod,
		ParentQualified: parent,
		LineStart:       extract.Line(n.StartPosition()),
		LineEnd:         extract.Line(n.EndPosition()),
	})
}

// ---- helpers ----

// qualify prepends the package name. If no package clause was found
// (unusual — a Go file almost always has one), fall back to the bare
// name rather than producing a leading-dot qualified form.
func (w *walker) qualify(name string) string {
	if w.pkg == "" {
		return name
	}
	return w.pkg + "." + name
}

// packageName reads the package clause from a source_file node. Zero
// value "" signals no package clause (malformed input or a top-level
// Go file that only has comments).
func packageName(root *sitter.Node, source []byte) string {
	if root == nil {
		return ""
	}
	for i := uint(0); i < root.NamedChildCount(); i++ {
		c := root.NamedChild(i)
		if c == nil || c.Kind() != "package_clause" {
			continue
		}
		for j := uint(0); j < c.NamedChildCount(); j++ {
			id := c.NamedChild(j)
			if id != nil && id.Kind() == "package_identifier" {
				return extract.Text(id, source)
			}
		}
	}
	return ""
}

// receiverType pulls the type name out of a method's receiver list.
// The receiver is a parameter_list containing one parameter_declaration
// whose `type` field is either a type_identifier (value receiver) or
// a pointer_type wrapping a type_identifier (pointer receiver). Type
// parameters (`Money[T]`) resolve through generic_type.
func receiverType(recv *sitter.Node, source []byte) string {
	if recv == nil {
		return ""
	}
	for i := uint(0); i < recv.NamedChildCount(); i++ {
		param := recv.NamedChild(i)
		if param == nil || param.Kind() != "parameter_declaration" {
			continue
		}
		t := param.ChildByFieldName("type")
		if t == nil {
			continue
		}
		return unwrapTypeName(t, source)
	}
	return ""
}

// unwrapTypeName peels pointer and generic wrappers off a type
// expression to get at the base type_identifier.
func unwrapTypeName(t *sitter.Node, source []byte) string {
	for t != nil {
		switch t.Kind() {
		case "type_identifier":
			return extract.Text(t, source)
		case "pointer_type":
			// `*T` has exactly one named child — the inner type.
			t = t.NamedChild(0)
		case "generic_type":
			if name := t.ChildByFieldName("type"); name != nil {
				t = name
				continue
			}
			return ""
		default:
			// Qualified types like `pkg.Type` (qualified_type node)
			// land here. Skip — cross-package resolution is 01-03's
			// job, not Tier-Basic's.
			return ""
		}
	}
	return ""
}
