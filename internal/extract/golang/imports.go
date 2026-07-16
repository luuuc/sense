package golang

// imports.go builds a file's import table: the in-file name each import
// binds → its full import path. This is the file-block layer of Go's scope
// nesting; typeinfer consults it AFTER function scope (locals/types) and
// resolution consults the PATH, never the in-file name, for stdlib/locality
// classification (an alias may shadow a stdlib name).
//
// Honesty contract: an entry here is a claim the resolver verifies
// independently (module prefix + directory + package clause). A name the
// table cannot infer uniquely produces NO entry: a miss means today's
// behavior, never a wrong bind.

import (
	"strconv"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
)

// collectImports walks the source file's import declarations into a
// name → path table. Dot imports and blank imports bind no per-name
// qualifier, so they produce no entry. Aliased imports key by the alias.
// Unaliased imports key by an inferred name: the path basename with the
// module-major (`…/mod/v2`) and gopkg.in (`yaml.v3`) version suffixes
// stripped. Two imports whose inferred names collide drop the key
// entirely (real Go would carry an alias; a non-unique guess is not
// evidence).
func collectImports(root *sitter.Node, source []byte) map[string]string {
	table := map[string]string{}
	collided := map[string]bool{}
	// import_spec nodes only occur inside import declarations, so a kind
	// walk needs no per-declaration structure handling (single vs grouped).
	_ = extract.WalkNamedDescendants(root, "import_spec", func(spec *sitter.Node) error {
		addImportSpec(spec, source, table, collided)
		return nil
	})
	return table
}

// addImportSpec records one import spec into the table, applying the
// dot/blank exclusions, alias precedence, and the collision-drops rule.
func addImportSpec(spec *sitter.Node, source []byte, table map[string]string, collided map[string]bool) {
	pathText := ""
	if pathNode := spec.ChildByFieldName("path"); pathNode != nil {
		pathText = extract.Text(pathNode, source)
	}
	// A missing path node unquotes as an error, so parser-tolerance shapes
	// and malformed literals share one rejection.
	path, err := strconv.Unquote(pathText)
	if err != nil || path == "" {
		return
	}
	name := ""
	if nameNode := spec.ChildByFieldName("name"); nameNode != nil {
		switch nameNode.Kind() {
		case "dot", "blank_identifier":
			return // no per-name qualifier is bound
		default:
			name = extract.Text(nameNode, source)
		}
	} else {
		name = inferImportName(path)
	}
	if name == "" {
		return
	}
	if collided[name] {
		return
	}
	if _, dup := table[name]; dup {
		delete(table, name) // a non-unique inferred name is not evidence
		collided[name] = true
		return
	}
	table[name] = path
}

// inferImportName guesses the in-file name of an unaliased import: the
// path's last segment, with a module-major suffix segment (`/v2`) or a
// gopkg.in-style dotted suffix (`yaml.v3`) stripped. The guess only has
// to be safe, not perfect: a mismatch with the real package clause is a
// table miss, and the resolver re-verifies module-local entries against
// the indexed package clause anyway.
func inferImportName(path string) string {
	base := path[strings.LastIndex(path, "/")+1:]
	if isVersionSegment(base) {
		if i := strings.LastIndex(path, "/"); i > 0 {
			rest := path[:i]
			base = rest[strings.LastIndex(rest, "/")+1:]
		}
	}
	if i := strings.LastIndex(base, "."); i > 0 && isVersionSegment(base[i+1:]) {
		base = base[:i]
	}
	return base
}

// isVersionSegment reports whether s is a version marker like "v2".
func isVersionSegment(s string) bool {
	if len(s) < 2 || s[0] != 'v' {
		return false
	}
	for _, r := range s[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
