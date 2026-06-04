package tsjs

import (
	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
)

// Export visibility is the TS/JS analog of Ruby's method-visibility pass and the
// structural capitalization rule Go/Rust get for free: it marks each emitted
// symbol `public` (exported in any form) or `private` (module-local). It is the
// unlock for two dead-code gates — the core voice's library-API gate and the
// TS voice's closed-world test — because a NON-exported symbol is reachable only
// within its own module, the one shape the module system proves closed-world.
//
// A symbol is `public` when its name is exported by any form the module system
// offers:
//
//   - `export function f` / `export class C` / `export const x` / `export
//     interface I` / `export type T` / `export enum E` — a declaration directly
//     under an `export_statement`.
//   - `export default …` — a default export (named or file-named anonymous).
//   - `export { g }` / `export { g as h }` — a clause naming a locally-declared
//     symbol elsewhere in the module (the LOCAL `name`, not the external alias).
//   - a re-exported name (`export { X } from './x'`) — handled inline by
//     handleReexport, which marks its synthesized symbols public directly.
//
// Everything else is `private` (module-local). The name-set approach can mark a
// nested member public when it collides with a top-level export name — that is
// the SAFE direction (a falsely-public symbol never earns `dead`, only loses
// recall), exactly the conservatism Ruby's visibility pass and Go's magic-method
// table choose.

// collectExports pre-scans the module for every export form and records the set
// of exported LOCAL names and, separately, the names that are default exports.
// Both feed the walker: `exported` drives each symbol's Visibility, and
// `defaultExports` is streamed by the harvest so the TS voice can raise the more
// specific ts_default_export reason. It also records whether the file is an ES
// module at all (see fileIsModule).
func (w *walker) collectExports(root *sitter.Node) {
	w.exported, w.defaultExports = collectExportedNames(root, w.source, w.filePath)
	w.isModule = fileIsModule(root)
}

// fileIsModule reports whether root is an ES module — a file with at least one
// top-level `import` or `export` statement. A file with neither is a global
// *script*: its top-level declarations share the global scope and are reachable
// by any other concatenated script (bundler runtimes, `/// <reference>` ambient
// files, classic `<script>`-style code), so "not exported" does NOT prove a
// symbol file-local. The dead-code closed-world bet is valid only for modules;
// in a script every symbol is treated as globally reachable (see visibilityOf).
// A dynamic `import()` is an expression, not a top-level import, so a file whose
// only import is dynamic is correctly a script.
func fileIsModule(root *sitter.Node) bool {
	for i := uint(0); i < root.NamedChildCount(); i++ {
		switch root.NamedChild(i).Kind() {
		case "import_statement", "export_statement":
			return true
		}
	}
	return false
}

// visibilityOf returns the visibility of a top-level symbol. In a script file
// (not an ES module) every symbol is globally reachable by concatenation, so it
// is "public" — never a closed-world dead candidate. In a module, a symbol is
// "public" iff it is in the exported set, else "private" (file-local).
func (w *walker) visibilityOf(name string) string {
	if !w.isModule {
		return "public"
	}
	if w.exported[name] {
		return "public"
	}
	return "private"
}

// methodVisibility returns the visibility of a method. In a script file it is
// "public" (global scope). In a module it is "public" when the method's enclosing
// top-level class is exported (so the method is reachable on the exported class
// from another module), else "private": a method is never itself named in an
// export clause, so its export-reachability tracks its class.
func (w *walker) methodVisibility(scope []string) string {
	if !w.isModule {
		return "public"
	}
	if len(scope) > 0 && w.exported[scope[0]] {
		return "public"
	}
	return "private"
}

// collectExportedNames walks every export_statement under root and returns the
// set of exported local names plus the subset that are default exports. It is a
// free function (not a walker method) so the harvest pass can reuse it without
// the walker's mutable state. filePath supplies the file-based name an anonymous
// default export is synthesized under (matching handleDefaultExport).
func collectExportedNames(root *sitter.Node, source []byte, filePath string) (exported, defaultExports map[string]bool) {
	exported = map[string]bool{}
	defaultExports = map[string]bool{}
	_ = extract.WalkNamedDescendants(root, "export_statement", func(es *sitter.Node) error {
		collectStatementExports(es, source, filePath, exported, defaultExports)
		return nil
	})
	return exported, defaultExports
}

// collectStatementExports records the local names one export_statement
// exports, into the shared exported/defaultExports sets. It handles the
// three local-export forms (clause, declaration, bare default expression)
// and skips re-exports, which name other modules' symbols and are owned by
// handleReexport.
func collectStatementExports(es *sitter.Node, source []byte, filePath string, exported, defaultExports map[string]bool) {
	// A re-export (`export { X } from './x'`, `export * from './x'`) names
	// symbols defined in another module, not local declarations —
	// handleReexport emits and marks those public itself. Skip it here so a
	// re-export clause's name is not mistaken for a local export.
	if reexportSource(es, source) != "" {
		return
	}
	isDefault := hasDefaultKeyword(es, source)

	collectClauseExports(es, source, exported)

	// `export [default] <declaration>`: a function/class/interface/enum/type
	// declaration or a `const` lexical declaration carried in the
	// `declaration` field.
	if decl := es.ChildByFieldName("declaration"); decl != nil {
		for _, name := range declaredNames(decl, source) {
			exported[name] = true
			if isDefault {
				defaultExports[name] = true
			}
		}
		return
	}

	// `export default <expr>`: a named identifier (`export default foo`) or an
	// anonymous function/class expression synthesized under the file-based name.
	if isDefault {
		if name := defaultExportName(es, source, filePath); name != "" {
			exported[name] = true
			defaultExports[name] = true
		}
	}
}

// collectClauseExports records the local names in an export_statement's
// `export { g }` / `export { g as h }` clauses: the LOCAL name is the
// specifier's `name` field (g), exported under the alias if present.
func collectClauseExports(es *sitter.Node, source []byte, exported map[string]bool) {
	for i := uint(0); i < es.NamedChildCount(); i++ {
		clause := es.NamedChild(i)
		if clause == nil || clause.Kind() != "export_clause" {
			continue
		}
		for j := uint(0); j < clause.NamedChildCount(); j++ {
			spec := clause.NamedChild(j)
			if spec == nil || spec.Kind() != "export_specifier" {
				continue
			}
			if name := extract.Text(spec.ChildByFieldName("name"), source); name != "" && name != "default" {
				exported[name] = true
			}
		}
	}
}

// declaredNames returns the names a declaration node introduces: the `name`
// field for a function/class/interface/enum/type-alias declaration, or every
// variable_declarator name for a `const` lexical declaration.
func declaredNames(decl *sitter.Node, source []byte) []string {
	switch decl.Kind() {
	case "lexical_declaration":
		var names []string
		for i := uint(0); i < decl.NamedChildCount(); i++ {
			d := decl.NamedChild(i)
			if d == nil || d.Kind() != "variable_declarator" {
				continue
			}
			if name := extract.Text(d.ChildByFieldName("name"), source); name != "" {
				names = append(names, name)
			}
		}
		return names
	default:
		if name := extract.Text(decl.ChildByFieldName("name"), source); name != "" {
			return []string{name}
		}
	}
	return nil
}

// defaultExportName returns the name a `export default <expr>` form binds, but
// ONLY for shapes the walker actually emits a symbol for: an identifier
// (`export default foo`) or an anonymous function/class expression synthesized
// under the file-based name. A literal or other expression (`export default 42`)
// emits no symbol, so it binds no name and returns "" — recording a file-based
// name there would be a phantom that no symbol matches.
func defaultExportName(es *sitter.Node, source []byte, filePath string) string {
	v := es.ChildByFieldName("value")
	if v == nil {
		return ""
	}
	switch v.Kind() {
	case "identifier", "type_identifier":
		return extract.Text(v, source)
	case "function_expression", "arrow_function", "class", "class_expression":
		return fileBasedNameOf(filePath)
	}
	return ""
}
