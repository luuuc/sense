package golang

import (
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// These tests cover receiver-type extraction for unusual-but-parseable
// receiver shapes, plus the nil-root guards on the package-scanning helpers.

// parseRecv parses a method declaration and returns its receiver node.
func parseRecv(t *testing.T, src string) (*sitter.Node, []byte) {
	t.Helper()
	ex := Extractor{}
	p := sitter.NewParser()
	t.Cleanup(p.Close)
	if err := p.SetLanguage(ex.Grammar()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	source := []byte(src)
	tree := p.Parse(source, nil)
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}
	t.Cleanup(tree.Close)
	md := tree.RootNode().NamedChild(1)
	recv := md.ChildByFieldName("receiver")
	if recv == nil {
		t.Fatal("method has no receiver node")
	}
	return recv, source
}

func TestReceiverTypeSkipsCommentChild(t *testing.T) {
	// A comment inside the receiver list is a non-parameter_declaration named
	// child; receiverType skips it and resolves the real receiver type.
	recv, src := parseRecv(t, "package p\nfunc (/* doc */ s Server) Run() {}\n")
	if got := receiverType(recv, src); got != "Server" {
		t.Errorf("receiverType = %q, want Server", got)
	}
}

func TestReceiverTypeCommentOnlyIsEmpty(t *testing.T) {
	// A receiver list containing only a comment has no parameter_declaration;
	// the loop completes and receiverType returns "".
	recv, src := parseRecv(t, "package p\nfunc (/* only */) Run() {}\n")
	if got := receiverType(recv, src); got != "" {
		t.Errorf("receiverType = %q, want empty", got)
	}
}

func TestMethodWithEmptyReceiverEmitsNothing(t *testing.T) {
	// Driven end-to-end: a comment-only receiver yields no resolvable type,
	// so handleMethod emits no method symbol.
	r := parse(t, "package p\nfunc (/* only */) Run() {}\n")
	for _, s := range r.symbols {
		if s.Kind == "method" {
			t.Errorf("unexpected method symbol for empty receiver: %v", s.Qualified)
		}
	}
}

func TestPackageNameNilRoot(t *testing.T) {
	// The documented nil-root guard returns the zero value.
	if got := packageName(nil, nil); got != "" {
		t.Errorf("packageName(nil) = %q, want empty", got)
	}
}

func TestWalkTopLevelNilRoot(t *testing.T) {
	// walkTopLevel's nil-root guard returns without error.
	w := &walker{source: nil, emit: &counter{}, pkgBindings: map[string]string{}}
	if err := w.walkTopLevel(nil); err != nil {
		t.Errorf("walkTopLevel(nil): %v", err)
	}
}
