package langspec

import (
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
)

// accessKeywords maps an access-modifier keyword to the visibility string the
// dead-code langspec voice reasons about. Only an explicit keyword maps; the
// per-grammar default (Java "package", C# "private", others "public") is applied
// by each grammar's fn when no keyword is present.
var accessKeywords = map[string]string{
	"public":    "public",
	"private":   "private",
	"protected": "protected",
	"internal":  "internal",
}

// modifierNodeKinds are the named node kinds that wrap a single access-modifier
// keyword as their TEXT (C# "modifier", Kotlin/PHP "visibility_modifier", Scala
// "access_modifier"). Java differs — its keyword is an unnamed token whose KIND is
// the keyword itself ("public"/"private"/"protected") — so accessModifierVisibility
// checks both a child's kind and, for these wrappers, its text.
var modifierNodeKinds = map[string]bool{
	"modifier":            true,
	"visibility_modifier": true,
	"access_modifier":     true,
}

// accessModifierVisibility scans a declaration node for an access-modifier
// keyword and returns the mapped visibility, or def when none is present. It
// looks at every direct child AND one level inside a "modifiers" wrapper, which
// together cover Java (unnamed keyword token inside `modifiers`), C# (named
// `modifier` direct children), Kotlin/Scala (a `visibility_modifier` /
// `access_modifier` inside `modifiers`), and PHP (a `visibility_modifier` direct
// child). The first keyword found wins; a declaration with several modifiers
// (`internal protected`) keeps the first, which is the safe direction (any
// non-"private" value only ever raises a hand).
func accessModifierVisibility(n *sitter.Node, source []byte, def string) string {
	if v := scanAccessTokens(n, source); v != "" {
		return v
	}
	count := n.NamedChildCount()
	for i := uint(0); i < count; i++ {
		child := n.NamedChild(i)
		if child != nil && child.Kind() == "modifiers" {
			if v := scanAccessTokens(child, source); v != "" {
				return v
			}
		}
	}
	return def
}

// scanAccessTokens returns the visibility of the first access-modifier token
// among n's direct children (named and unnamed), or "" when none is present. A
// child matches by kind (Java's `public` token node) or, for a modifier-wrapper
// kind, by its text (C#/Kotlin/Scala/PHP).
func scanAccessTokens(n *sitter.Node, source []byte) string {
	count := n.ChildCount()
	for i := uint(0); i < count; i++ {
		child := n.Child(i)
		if child == nil {
			continue
		}
		if v := accessKeywords[child.Kind()]; v != "" {
			return v
		}
		if modifierNodeKinds[child.Kind()] {
			if v := accessKeywords[strings.TrimSpace(extract.Text(child, source))]; v != "" {
				return v
			}
		}
	}
	return ""
}

// javaVisibility reads a Java declaration's access modifier. The default — no
// access keyword — is package-private, which Java exposes to the rest of its
// package; it is NOT file-local, so it maps to "package" (not "private") and the
// voice keeps it open-world. Only an explicit `private` member is file-local and
// thus `dead`-eligible.
func javaVisibility(n *sitter.Node, source []byte) string {
	return accessModifierVisibility(n, source, "package")
}

// csharpVisibility reads a C# declaration's access modifier. With no explicit
// modifier the default is kind-dependent: a class member (method/constructor)
// defaults to private, but a namespace is global and a top-level type is
// internal — neither is file-local nor cleanly public, so those map to "" rather
// than a guessed token (the voice treats "" as not-file-local and raises a hand).
func csharpVisibility(n *sitter.Node, source []byte) string {
	if v := accessModifierVisibility(n, source, ""); v != "" {
		return v
	}
	switch n.Kind() {
	case "method_declaration", "constructor_declaration":
		return "private"
	}
	return ""
}

// kotlinVisibility reads a Kotlin declaration's access modifier; Kotlin defaults
// to public when none is present.
func kotlinVisibility(n *sitter.Node, source []byte) string {
	return accessModifierVisibility(n, source, "public")
}

// scalaVisibility reads a Scala declaration's access modifier; Scala defaults to
// public when none is present.
func scalaVisibility(n *sitter.Node, source []byte) string {
	return accessModifierVisibility(n, source, "public")
}

// phpVisibility reads a PHP method's visibility modifier; a method (and any
// top-level function) defaults to public when none is present.
func phpVisibility(n *sitter.Node, source []byte) string {
	return accessModifierVisibility(n, source, "public")
}

// cVisibility maps C's storage class to visibility: a `static` function is
// file-local ("private"); everything else has external linkage ("public"). C has
// no other access concept.
func cVisibility(n *sitter.Node, source []byte) string {
	count := n.NamedChildCount()
	for i := uint(0); i < count; i++ {
		child := n.NamedChild(i)
		if child != nil && child.Kind() == "storage_class_specifier" &&
			strings.TrimSpace(extract.Text(child, source)) == "static" {
			return "private"
		}
	}
	return "public"
}

// cppVisibility maps a C++ member function's positional access section to
// visibility. Members live in a field_declaration_list with `access_specifier`
// markers (`public:` / `private:` / `protected:`); the nearest specifier before
// the node sets visibility. With no specifier, the enclosing aggregate's default
// applies — private for a class, public for a struct. A function defined outside a
// class body (a free function, or an out-of-line `Foo::bar` definition) has no
// enclosing field_declaration_list and is treated as public.
func cppVisibility(n *sitter.Node, source []byte) string {
	parent := n.Parent()
	if parent == nil || parent.Kind() != "field_declaration_list" {
		return "public"
	}
	for sib := n.PrevSibling(); sib != nil; sib = sib.PrevSibling() {
		if sib.Kind() == "access_specifier" {
			if v := accessKeywords[accessSpecifierKeyword(sib, source)]; v != "" {
				return v
			}
		}
	}
	if grand := parent.Parent(); grand != nil && grand.Kind() == "struct_specifier" {
		return "public"
	}
	return "private"
}

// accessSpecifierKeyword returns the leading keyword of a C++ access_specifier
// node, stripping the trailing colon and any whitespace (`public:` → "public").
func accessSpecifierKeyword(n *sitter.Node, source []byte) string {
	text := strings.TrimSpace(extract.Text(n, source))
	if i := strings.IndexByte(text, ':'); i >= 0 {
		text = text[:i]
	}
	return strings.TrimSpace(text)
}
