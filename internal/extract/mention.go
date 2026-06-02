package extract

import (
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// MentionWalkSpec parameterizes the shared mention harvest for one grammar. The
// harvest itself — visit every identifier / name-literal node, skip the one
// that is a definition's own name, collect the rest — is language-agnostic; only
// the node kinds and the definition-name test differ per grammar. A voice's
// extractor supplies those here so it opts into the soundness harvest with a few
// lines rather than re-implementing the tree-walk.
type MentionWalkSpec struct {
	// NameOf maps a tree-sitter node kind to the function that reads the bare
	// name off a node of that kind (e.g. Ruby "identifier" → Text, and
	// "simple_symbol" → strip the leading colon). A node whose kind is absent
	// from the map is never visited, so the spec is also the allow-list of which
	// kinds carry a mention. At least one kind must be supplied or the harvest is
	// empty.
	NameOf map[string]func(*sitter.Node, []byte) string
	// SkipDefinitionName reports whether a node is a definition's OWN name (the
	// `foo` token in Ruby `def foo`). Such a node must NOT count as a mention —
	// otherwise a method would cancel its own `dead` candidacy and no symbol
	// could ever earn `dead`, muting the verdict (safe but useless). It is
	// per-grammar because "what is a definition node" differs by language. A nil
	// predicate skips nothing. It is applied to every visited node regardless of
	// kind; a kind that can never be a definition name simply never matches.
	SkipDefinitionName func(*sitter.Node) bool
}

// HarvestMentions walks root and returns the deduplicated set of bare names it
// mentions anywhere EXCEPT a definition's own name, per the grammar-specific
// spec. It is the shared core of the dead-code soundness harvest: a candidate
// earns `dead` only when its name is absent from this set, so the set must be
// the broad superset of every position a hidden caller could leave a textual
// trace (a bare call, a chain receiver, a `**splat` arg, a `:symbol` literal).
// Names are deduplicated; order is unspecified (the caller streams them into a
// set). An empty harvest returns nil so callers can cheaply test for "nothing".
func HarvestMentions(root *sitter.Node, source []byte, spec MentionWalkSpec) []string {
	seen := map[string]struct{}{}
	for kind, nameOf := range spec.NameOf {
		_ = WalkNamedDescendants(root, kind, func(n *sitter.Node) error {
			if spec.SkipDefinitionName != nil && spec.SkipDefinitionName(n) {
				return nil
			}
			if name := nameOf(n, source); name != "" {
				seen[name] = struct{}{}
			}
			return nil
		})
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	return out
}
