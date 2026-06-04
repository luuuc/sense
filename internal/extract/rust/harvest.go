package rust

import (
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
)

// HarvestsMentions reports that the Rust extractor streams the broad mention set
// (see emitHarvest), so the scan records `rust` as harvested even on a scan that
// yields zero mentions — the dead-code soundness gate then treats a Rust symbol
// as proven-against-an-empty-set, not never-harvested. Without this opt-in, every
// Rust symbol would fail closed at the per-language soundness gate
// (core_no_harvest) and no Rust symbol could ever earn `dead`.
func (Extractor) HarvestsMentions() bool { return true }

// emitHarvest streams the file's broad mention set, reflective dispatch targets,
// FFI/`#[used]` export names, and test-symbol names to the emitter when it accepts
// them. The mention set feeds the arbiter's soundness gate (a symbol earns `dead`
// only when its bare name is mentioned nowhere a hidden caller could be); the
// dispatch set feeds the core voice's reflection gate (a type named only in an
// `Any::downcast::<T>()` turbofish is reached dynamically); the export and test
// sets feed the Rust voice's rust_ffi / rust_used / rust_test reasons. All four
// are gathered in one tree walk; each emit is best-effort — an Emitter that
// implements neither extension simply receives no names.
func emitHarvest(root *sitter.Node, source []byte, emit extract.Emitter) error {
	h := collectRustHarvest(root, source)
	if de, ok := emit.(extract.DispatchEmitter); ok {
		if err := emitEach(h.dispatch, de.DispatchName); err != nil {
			return err
		}
	}
	if me, ok := emit.(extract.MentionEmitter); ok {
		if err := emitEach(h.mentions, me.MentionName); err != nil {
			return err
		}
	}
	if re, ok := emit.(extract.RustHarvestEmitter); ok {
		if err := emitRustHarvest(re, h); err != nil {
			return err
		}
	}
	return nil
}

// emitRustHarvest streams the Rust-specific name sets (FFI/used exports,
// test symbols, trait-impl methods, allow(dead) names) to a
// RustHarvestEmitter, stopping at the first emit error.
func emitRustHarvest(re extract.RustHarvestEmitter, h rustHarvest) error {
	if err := emitEach(h.exports, re.RustExportName); err != nil {
		return err
	}
	if err := emitEach(h.testSymbols, re.RustTestSymbol); err != nil {
		return err
	}
	if err := emitEach(h.traitImplMethods, re.RustTraitImplMethod); err != nil {
		return err
	}
	return emitEach(h.allowDead, re.RustAllowDeadName)
}

// emitEach calls fn for every name in the set, stopping at the first
// error. Map iteration order is unspecified, but each name set is emitted
// independently, so order does not affect the harvested result.
func emitEach(names map[string]struct{}, fn func(string) error) error {
	for name := range names {
		if err := fn(name); err != nil {
			return err
		}
	}
	return nil
}

// rustHarvest accumulates the name sets the Rust dead-code analysis needs:
//   - mentions: every identifier / type / field token EXCEPT a definition's own
//     name — the broad superset feeding the soundness gate.
//   - dispatch: type names reached via `Any::downcast::<T>()` — the only common
//     Rust idiom that names a type the static graph cannot see.
//   - exports: function names marked `#[no_mangle]` / `#[export_name]` and static
//     names marked `#[no_mangle]` / `#[used]`, called/kept-alive with no Rust
//     caller (rust_ffi for functions, rust_used for statics).
//   - testSymbols: names of items marked `#[test]` / `#[bench]` or nested under a
//     `#[cfg(test)]` module, invoked only by the test harness (rust_test).
//   - traitImplMethods: names of methods defined in an `impl Trait for Type`
//     block. Such a method satisfies a trait and is reached through a trait object
//     or generic bound, where the static graph shows no direct caller — the sound,
//     name-independent trait-impl signal (it sees serde's `deserialize_*`, `Write`'s
//     `flush`, and any other external trait the magic table cannot enumerate).
//   - allowDead: names of items annotated `#[allow(dead_code)]` / `#[allow(unused)]`.
//     The author deliberately suppressed the lint, so rustc never warns them and
//     they are absent from the cargo oracle (rust_allow_dead).
type rustHarvest struct {
	mentions         map[string]struct{}
	dispatch         map[string]struct{}
	exports          map[string]struct{}
	testSymbols      map[string]struct{}
	traitImplMethods map[string]struct{}
	allowDead        map[string]struct{}
}

// collectRustHarvest gathers every name set in two passes: the shared,
// grammar-parameterised mention walk (HarvestMentions), and an attribute-aware
// recursive walk that tracks `#[cfg(test)]` scope to route export and test names.
// Scan is not a hot path, so two clear passes beat one entangled one.
func collectRustHarvest(root *sitter.Node, source []byte) rustHarvest {
	h := rustHarvest{
		mentions:         map[string]struct{}{},
		dispatch:         map[string]struct{}{},
		exports:          map[string]struct{}{},
		testSymbols:      map[string]struct{}{},
		traitImplMethods: map[string]struct{}{},
		allowDead:        map[string]struct{}{},
	}
	for _, name := range extract.HarvestMentions(root, source, mentionWalkSpec()) {
		h.mentions[name] = struct{}{}
	}
	h.walkAttrs(root, false, source)
	h.collectDispatch(root, source)
	h.collectTraitImplMethods(root, source)
	return h
}

// collectTraitImplMethods records the name of every method defined in an
// `impl Trait for Type` block (an impl_item carrying a `trait` field). Such a
// method satisfies the trait and is reached through it, never by a direct caller
// the static graph can see. This is the sound, name-independent backstop for the
// trait-impl reason: it covers external traits (serde's Deserializer, std's Write,
// Iterator, …) the magic table cannot enumerate.
func (h rustHarvest) collectTraitImplMethods(root *sitter.Node, source []byte) {
	_ = extract.WalkNamedDescendants(root, "impl_item", func(impl *sitter.Node) error {
		if impl.ChildByFieldName("trait") == nil {
			return nil
		}
		body := impl.ChildByFieldName("body")
		if body == nil {
			return nil
		}
		for i := uint(0); i < body.NamedChildCount(); i++ {
			child := body.NamedChild(i)
			if child == nil || child.Kind() != "function_item" {
				continue
			}
			if name := extract.Text(child.ChildByFieldName("name"), source); name != "" {
				h.traitImplMethods[name] = struct{}{}
			}
		}
		return nil
	})
}

// mentionWalkSpec is Rust's grammar parameterisation of the shared mention
// harvest: identifiers, type identifiers, and field identifiers carry a mention,
// and a definition's own name token is excluded so a symbol is never cancelled by
// its own declaration. Trait method signatures (function_signature_item) are
// deliberately NOT excluded: leaving a declared trait method's name in the
// mention set keeps a same-named concrete impl open-world even when the resolver
// never drew the satisfaction edge — the trait-impl soundness backstop.
func mentionWalkSpec() extract.MentionWalkSpec {
	return extract.MentionWalkSpec{
		NameOf: map[string]func(*sitter.Node, []byte) string{
			"identifier":                 extract.Text,
			"type_identifier":            extract.Text,
			"field_identifier":           extract.Text,
			"shorthand_field_identifier": extract.Text,
		},
		SkipDefinitionName: isRustDefinitionName,
	}
}

// isRustDefinitionName reports whether n is the `name` field of a concrete
// definition (function / struct / enum / trait / type-alias / const / static /
// mod). Those tokens are excluded from the mention set. function_signature_item
// (a trait method declaration) is intentionally absent — see mentionWalkSpec.
func isRustDefinitionName(n *sitter.Node) bool {
	p := n.Parent()
	if p == nil {
		return false
	}
	switch p.Kind() {
	case "function_item", "struct_item", "enum_item", "trait_item",
		"type_item", "const_item", "static_item", "mod_item":
		name := p.ChildByFieldName("name")
		return name != nil && name.Id() == n.Id()
	}
	return false
}

// walkAttrs recurses the tree tracking whether the current node is inside a
// `#[cfg(test)]` module, routing each definition's name to the export or test set
// per its attributes. inTest is sticky: every descendant of a `#[cfg(test)]`
// module is a test symbol regardless of its own attributes.
func (h rustHarvest) walkAttrs(n *sitter.Node, inTest bool, source []byte) {
	count := n.NamedChildCount()
	for i := uint(0); i < count; i++ {
		child := n.NamedChild(i)
		if child == nil {
			continue
		}
		childInTest := inTest || hasCfgTest(child, source)
		switch child.Kind() {
		case "function_item":
			h.classifyFunction(child, childInTest, source)
		case "static_item", "const_item":
			h.classifyStatic(child, childInTest, source)
		case "struct_item", "enum_item", "trait_item", "type_item":
			if childInTest {
				h.recordName(child, h.testSymbols, source)
			}
		}
		// An item the author annotated `#[allow(dead_code)]` is intentionally
		// retained; rustc suppresses the lint, so it never reaches the cargo oracle.
		if isDefinitionKind(child.Kind()) && hasAllowDead(child, source) {
			h.recordName(child, h.allowDead, source)
		}
		h.walkAttrs(child, childInTest, source)
	}
}

// classifyFunction routes a function definition to the test set (inside
// `#[cfg(test)]`, or marked `#[test]` / `#[bench]`) or the export set (marked
// `#[no_mangle]` / `#[export_name]`). Test classification wins: a `#[no_mangle]`
// function inside a test module is still test-only.
func (h rustHarvest) classifyFunction(fn *sitter.Node, inTest bool, source []byte) {
	if inTest || hasAttr(fn, source, "test", "bench") {
		h.recordName(fn, h.testSymbols, source)
		return
	}
	if hasAttr(fn, source, "no_mangle", "export_name") {
		h.recordName(fn, h.exports, source)
	}
}

// classifyStatic routes a static / const to the test set (inside `#[cfg(test)]`)
// or the export set (marked `#[no_mangle]` / `#[export_name]` / `#[used]`, kept
// alive by the linker with no Rust reader).
func (h rustHarvest) classifyStatic(s *sitter.Node, inTest bool, source []byte) {
	if inTest {
		h.recordName(s, h.testSymbols, source)
		return
	}
	if hasAttr(s, source, "no_mangle", "export_name", "used") {
		h.recordName(s, h.exports, source)
	}
}

// recordName adds a definition node's bare name to set.
func (h rustHarvest) recordName(n *sitter.Node, set map[string]struct{}, source []byte) {
	if name := extract.Text(n.ChildByFieldName("name"), source); name != "" {
		set[name] = struct{}{}
	}
}

// collectDispatch records the type argument of every `.downcast::<T>()` /
// `downcast_ref::<T>()` / `downcast_mut::<T>()` call as a reflection-reached type
// name — the one common Rust idiom (`std::any::Any`) that names a type the static
// graph cannot tie back to a caller.
func (h rustHarvest) collectDispatch(root *sitter.Node, source []byte) {
	_ = extract.WalkNamedDescendants(root, "generic_function", func(g *sitter.Node) error {
		fn := g.ChildByFieldName("function")
		if !isDowncastCallee(fn, source) {
			return nil
		}
		args := g.ChildByFieldName("type_arguments")
		if args == nil {
			return nil
		}
		for i := uint(0); i < args.NamedChildCount(); i++ {
			if name := unwrapTypeName(args.NamedChild(i), source); name != "" {
				h.dispatch[name] = struct{}{}
			}
		}
		return nil
	})
}

// isDowncastCallee reports whether fn is the callee of an `Any` downcast — a bare
// `downcast` path or a `x.downcast_ref` field access.
func isDowncastCallee(fn *sitter.Node, source []byte) bool {
	if fn == nil {
		return false
	}
	switch fn.Kind() {
	case "identifier":
		return downcastMethods[extract.Text(fn, source)]
	case "field_expression":
		return downcastMethods[extract.Text(fn.ChildByFieldName("field"), source)]
	}
	return false
}

// downcastMethods are the std `Any` downcast methods whose turbofish type
// argument names a dynamically-reached type.
var downcastMethods = map[string]bool{
	"downcast": true, "downcast_ref": true, "downcast_mut": true,
}

// hasAttr reports whether any attribute immediately preceding n names one of the
// given attribute names, matching on the path's LAST segment so a scoped test
// attribute like `#[tokio::test]` / `#[async_std::test]` matches "test" just as a
// bare `#[test]` does.
func hasAttr(n *sitter.Node, source []byte, names ...string) bool {
	found := false
	forEachPrecedingAttr(n, func(attr *sitter.Node) {
		name := attrLastSegment(attr, source)
		for _, want := range names {
			if name == want {
				found = true
			}
		}
	})
	return found
}

// attrLastSegment returns the last `::`-separated segment of an attribute's path,
// so `#[tokio::test]` → "test" and a bare `#[no_mangle]` → "no_mangle".
func attrLastSegment(attr *sitter.Node, source []byte) string {
	text := extract.Text(attr.NamedChild(0), source)
	if i := strings.LastIndex(text, "::"); i >= 0 {
		return text[i+len("::"):]
	}
	return text
}

// isDefinitionKind reports whether a node kind is a Rust definition that can
// carry a name and become a dead-code candidate.
func isDefinitionKind(kind string) bool {
	switch kind {
	case "function_item", "static_item", "const_item",
		"struct_item", "enum_item", "trait_item", "type_item", "mod_item":
		return true
	}
	return false
}

// hasAllowDead reports whether n carries an `#[allow(dead_code)]` or
// `#[allow(unused)]` attribute (including grouped `#[allow(dead_code, …)]`): an
// `allow` attribute whose token tree mentions `dead_code` or `unused`.
func hasAllowDead(n *sitter.Node, source []byte) bool {
	found := false
	forEachPrecedingAttr(n, func(attr *sitter.Node) {
		if attrLastSegment(attr, source) != "allow" {
			return
		}
		_ = extract.WalkNamedDescendants(attr, "identifier", func(id *sitter.Node) error {
			switch extract.Text(id, source) {
			case "dead_code", "unused":
				found = true
			}
			return nil
		})
	})
	return found
}

// hasCfgTest reports whether n carries a `#[cfg(test)]` attribute (including
// `#[cfg(all(test, …))]`): a `cfg` attribute whose token tree mentions `test`.
func hasCfgTest(n *sitter.Node, source []byte) bool {
	found := false
	forEachPrecedingAttr(n, func(attr *sitter.Node) {
		if extract.Text(attr.NamedChild(0), source) != "cfg" {
			return
		}
		_ = extract.WalkNamedDescendants(attr, "identifier", func(id *sitter.Node) error {
			if extract.Text(id, source) == "test" {
				found = true
			}
			return nil
		})
	})
	return found
}

// forEachPrecedingAttr calls fn for the `attribute` node of every `attribute_item`
// sibling immediately preceding n. Rust attaches outer attributes as prior
// siblings, so the scan stops at the first non-attribute sibling.
func forEachPrecedingAttr(n *sitter.Node, fn func(attr *sitter.Node)) {
	for sib := n.PrevNamedSibling(); sib != nil; sib = sib.PrevNamedSibling() {
		if sib.Kind() != "attribute_item" {
			break
		}
		inner := sib.NamedChild(0)
		if inner != nil && inner.Kind() == "attribute" {
			fn(inner)
		}
	}
}
