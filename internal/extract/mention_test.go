package extract_test

import (
	"strings"
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/grammars"
)

// parseRuby parses raw Ruby bytes and returns the root node, cleaning up the
// parser and tree when the test ends. The walker is grammar-agnostic; Ruby is
// just a convenient real grammar to drive it on raw bytes.
func parseRuby(t *testing.T, src string) *sitter.Node {
	t.Helper()
	p := sitter.NewParser()
	t.Cleanup(p.Close)
	if err := p.SetLanguage(grammars.Ruby()); err != nil {
		t.Fatalf("set language: %v", err)
	}
	tree := p.Parse([]byte(src), nil)
	t.Cleanup(tree.Close)
	return tree.RootNode()
}

// isMethodDefName reports whether n is the `name` token of a Ruby method def —
// a stand-in for the per-grammar definition-name test a spec supplies.
func isMethodDefName(n *sitter.Node) bool {
	parent := n.Parent()
	if parent == nil {
		return false
	}
	if parent.Kind() != "method" && parent.Kind() != "singleton_method" {
		return false
	}
	name := parent.ChildByFieldName("name")
	return name != nil && name.Equals(*n)
}

func stripColon(n *sitter.Node, src []byte) string {
	return strings.TrimPrefix(extract.Text(n, src), ":")
}

func toSet(names []string) map[string]struct{} {
	out := make(map[string]struct{}, len(names))
	for _, n := range names {
		out[n] = struct{}{}
	}
	return out
}

// TestHarvestMentions drives the shared walker on raw bytes: every identifier
// and symbol literal is collected except a definition's own name, across the
// call-position forms a resolver can fail to bind (a macro symbol arg, a bare
// call, a chain receiver).
func TestHarvestMentions(t *testing.T) {
	src := "class Form\n" +
		"  validate :amount_ok\n" + // symbol arg to an unrecognized macro
		"  def run\n" +
		"    helper_call\n" + // bare call
		"    obj.chain_recv\n" + // chain receiver
		"  end\n" +
		"  def lonely; end\n" + // defined, mentioned nowhere else
		"end\n"
	root := parseRuby(t, src)

	got := extract.HarvestMentions(root, []byte(src), extract.MentionWalkSpec{
		NameOf: map[string]func(*sitter.Node, []byte) string{
			"identifier":    extract.Text,
			"simple_symbol": stripColon,
		},
		SkipDefinitionName: isMethodDefName,
	})
	set := toSet(got)

	for _, want := range []string{"amount_ok", "helper_call", "chain_recv", "obj"} {
		if _, ok := set[want]; !ok {
			t.Errorf("HarvestMentions missing %q (got %v)", want, got)
		}
	}
	// A method's own name is excluded, so a method mentioned nowhere else is
	// absent — otherwise no method could ever earn `dead`.
	for _, defName := range []string{"lonely", "run"} {
		if _, ok := set[defName]; ok {
			t.Errorf("definition name %q must be excluded (got %v)", defName, got)
		}
	}
}

// TestHarvestMentionsRespectsSpecParameters proves the seam is parameterized,
// not hardcoded to Ruby: dropping SkipDefinitionName lets a definition name
// through, and an empty NameOf visits nothing. A future voice relies on exactly
// this — its own node kinds and definition test, not Ruby's.
func TestHarvestMentionsRespectsSpecParameters(t *testing.T) {
	src := "def only_def; end\n"
	root := parseRuby(t, src)

	// No skip predicate → the definition name now appears as a "mention".
	withDefs := toSet(extract.HarvestMentions(root, []byte(src), extract.MentionWalkSpec{
		NameOf: map[string]func(*sitter.Node, []byte) string{"identifier": extract.Text},
	}))
	if _, ok := withDefs["only_def"]; !ok {
		t.Errorf("with no SkipDefinitionName, 'only_def' should appear (got %v)", withDefs)
	}

	// Empty NameOf → no kind is visited, so the harvest is nil.
	if got := extract.HarvestMentions(root, []byte(src), extract.MentionWalkSpec{}); got != nil {
		t.Errorf("empty spec should harvest nothing, got %v", got)
	}
}
