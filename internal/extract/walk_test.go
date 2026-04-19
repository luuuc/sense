package extract_test

import (
	"errors"
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/grammars"
)

// TestWalkNamedDescendants covers the three behaviours the shared
// helper guarantees: visits land in document order, matched nodes are
// both visited and recursed through (so nested matches both fire),
// and a visitor error aborts the walk. The Go grammar is used only
// for its tree shape — any language would do.
func TestWalkNamedDescendants(t *testing.T) {
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(grammars.Go()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}

	src := []byte(`package p

func F() {
	a()
	b(c())
}
`)
	tree := parser.Parse(src, nil)
	if tree == nil {
		t.Fatal("Parse returned nil")
	}
	defer tree.Close()

	t.Run("visits all matches including nested", func(t *testing.T) {
		var seen []string
		err := extract.WalkNamedDescendants(tree.RootNode(), "call_expression", func(n *sitter.Node) error {
			seen = append(seen, n.Utf8Text(src))
			return nil
		})
		if err != nil {
			t.Fatalf("walk: %v", err)
		}
		// F's body calls a(), then b(c()). b(c()) nests: outer call b(c()),
		// inner call c(). Three visits total, outer-before-inner for the
		// nested pair (depth-first, matched node recursed through).
		want := []string{"a()", "b(c())", "c()"}
		if len(seen) != len(want) {
			t.Fatalf("visits = %v, want %v", seen, want)
		}
		for i, got := range seen {
			if got != want[i] {
				t.Errorf("visit[%d] = %q, want %q", i, got, want[i])
			}
		}
	})

	t.Run("nil node is a no-op", func(t *testing.T) {
		err := extract.WalkNamedDescendants(nil, "call_expression", func(*sitter.Node) error {
			t.Fatal("visitor called on nil walk")
			return nil
		})
		if err != nil {
			t.Errorf("nil walk returned err: %v", err)
		}
	})

	t.Run("visitor error aborts", func(t *testing.T) {
		boom := errors.New("boom")
		var visits int
		err := extract.WalkNamedDescendants(tree.RootNode(), "call_expression", func(*sitter.Node) error {
			visits++
			return boom
		})
		if !errors.Is(err, boom) {
			t.Errorf("err = %v, want %v", err, boom)
		}
		if visits != 1 {
			t.Errorf("visits = %d, want 1 (walk should abort on first error)", visits)
		}
	})

	t.Run("unmatched kind visits nothing", func(t *testing.T) {
		err := extract.WalkNamedDescendants(tree.RootNode(), "this_kind_does_not_exist", func(*sitter.Node) error {
			t.Fatal("visitor called on unmatched kind")
			return nil
		})
		if err != nil {
			t.Errorf("unmatched walk returned err: %v", err)
		}
	})
}
