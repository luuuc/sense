package langspec

import (
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/grammars"
)

// findFirst returns the first node of the given kind in a pre-order walk, or nil.
func findFirst(n *sitter.Node, kind string) *sitter.Node {
	if n.Kind() == kind {
		return n
	}
	count := n.ChildCount()
	for i := uint(0); i < count; i++ {
		if hit := findFirst(n.Child(i), kind); hit != nil {
			return hit
		}
	}
	return nil
}

// findNamed returns the first declaration node of the given kind whose NameField
// (or, for grammars without one, whose scanned identifier) text equals name.
func findNamed(t *testing.T, n *sitter.Node, kind, nameField, name string, source []byte) *sitter.Node {
	t.Helper()
	var hit *sitter.Node
	var walk func(*sitter.Node)
	walk = func(node *sitter.Node) {
		if hit != nil {
			return
		}
		if node.Kind() == kind {
			if nm := node.ChildByFieldName(nameField); nm != nil && nm.Utf8Text(source) == name {
				hit = node
				return
			}
		}
		count := node.ChildCount()
		for i := uint(0); i < count; i++ {
			walk(node.Child(i))
		}
	}
	walk(n)
	if hit == nil {
		t.Fatalf("no %s named %q found", kind, name)
	}
	return hit
}

func TestJavaVisibility(t *testing.T) {
	src := []byte(`public class Foo {
    public void pub() {}
    private void priv() {}
    protected void prot() {}
    void pkg() {}
}`)
	tree := parse(t, grammars.Java(), string(src))
	root := tree.RootNode()
	cases := map[string]string{"pub": "public", "priv": "private", "prot": "protected", "pkg": "package"}
	for method, want := range cases {
		n := findNamed(t, root, "method_declaration", "name", method, src)
		if got := javaVisibility(n, src); got != want {
			t.Errorf("javaVisibility(%s) = %q, want %q", method, got, want)
		}
	}
	// A public top-level class is public.
	cls := findNamed(t, root, "class_declaration", "name", "Foo", src)
	if got := javaVisibility(cls, src); got != "public" {
		t.Errorf("javaVisibility(class Foo) = %q, want public", got)
	}
}

func TestCSharpVisibility(t *testing.T) {
	src := []byte(`class Foo {
    public void Pub() {}
    private void Priv() {}
    protected void Prot() {}
    internal void Int() {}
    void Def() {}
}`)
	root := parse(t, grammars.CSharp(), string(src)).RootNode()
	cases := map[string]string{"Pub": "public", "Priv": "private", "Prot": "protected", "Int": "internal", "Def": "private"}
	for method, want := range cases {
		n := findNamed(t, root, "method_declaration", "name", method, src)
		if got := csharpVisibility(n, src); got != want {
			t.Errorf("csharpVisibility(%s) = %q, want %q", method, got, want)
		}
	}
}

func TestKotlinVisibility(t *testing.T) {
	src := []byte(`class Foo {
    fun pub() {}
    private fun priv() {}
    protected fun prot() {}
    internal fun int() {}
}`)
	root := parse(t, grammars.Kotlin(), string(src)).RootNode()
	cases := map[string]string{"pub": "public", "priv": "private", "prot": "protected", "int": "internal"}
	// fwcd/tree-sitter-kotlin has no "name" field; the function name is a
	// simple_identifier child. Collect each function_declaration by that name.
	got := map[string]string{}
	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		if n.Kind() == "function_declaration" {
			if id := findFirst(n, "simple_identifier"); id != nil {
				got[id.Utf8Text(src)] = kotlinVisibility(n, src)
			}
		}
		count := n.ChildCount()
		for i := uint(0); i < count; i++ {
			walk(n.Child(i))
		}
	}
	walk(root)
	for fn, want := range cases {
		if got[fn] != want {
			t.Errorf("kotlinVisibility(%s) = %q, want %q", fn, got[fn], want)
		}
	}
}

func TestScalaVisibility(t *testing.T) {
	src := []byte(`class Foo {
    def pub(): Unit = {}
    private def priv(): Unit = {}
    protected def prot(): Unit = {}
}`)
	root := parse(t, grammars.Scala(), string(src)).RootNode()
	cases := map[string]string{"pub": "public", "priv": "private", "prot": "protected"}
	for fn, want := range cases {
		n := findNamed(t, root, "function_definition", "name", fn, src)
		if got := scalaVisibility(n, src); got != want {
			t.Errorf("scalaVisibility(%s) = %q, want %q", fn, got, want)
		}
	}
}

func TestPHPVisibility(t *testing.T) {
	src := []byte(`<?php class Foo {
    public function pub() {}
    private function priv() {}
    protected function prot() {}
    function def() {}
}`)
	root := parse(t, grammars.PHP(), string(src)).RootNode()
	cases := map[string]string{"pub": "public", "priv": "private", "prot": "protected", "def": "public"}
	for method, want := range cases {
		n := findNamed(t, root, "method_declaration", "name", method, src)
		if got := phpVisibility(n, src); got != want {
			t.Errorf("phpVisibility(%s) = %q, want %q", method, got, want)
		}
	}
}

func TestCVisibility(t *testing.T) {
	src := []byte(`static int priv(void) { return 0; }
int pub(void) { return 1; }`)
	root := parse(t, grammars.C(), string(src)).RootNode()
	count := root.ChildCount()
	got := map[string]string{}
	for i := uint(0); i < count; i++ {
		fn := root.Child(i)
		if fn.Kind() != "function_definition" {
			continue
		}
		decl := findFirst(fn, "identifier")
		got[decl.Utf8Text(src)] = cVisibility(fn, src)
	}
	if got["priv"] != "private" {
		t.Errorf("cVisibility(static priv) = %q, want private", got["priv"])
	}
	if got["pub"] != "public" {
		t.Errorf("cVisibility(extern pub) = %q, want public", got["pub"])
	}
}

func TestCppVisibility(t *testing.T) {
	src := []byte(`class Cls {
    void implicitPriv() {}
public:
    void pub() {}
private:
    void priv() {}
protected:
    void prot() {}
};
struct Str {
    void implicitPub() {}
};
void freeFn() {}`)
	root := parse(t, grammars.Cpp(), string(src)).RootNode()
	cases := map[string]string{
		"implicitPriv": "private", // class default
		"pub":          "public",
		"priv":         "private",
		"prot":         "protected",
		"implicitPub":  "public", // struct default
		"freeFn":       "public", // free function
	}
	// function_definition nodes carry the bodies; find each by its declarator name.
	var got = map[string]string{}
	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		if n.Kind() == "function_definition" {
			if id := findFirst(n, "field_identifier"); id != nil {
				got[id.Utf8Text(src)] = cppVisibility(n, src)
			} else if id := findFirst(n, "identifier"); id != nil {
				got[id.Utf8Text(src)] = cppVisibility(n, src)
			}
		}
		count := n.ChildCount()
		for i := uint(0); i < count; i++ {
			walk(n.Child(i))
		}
	}
	walk(root)
	for name, want := range cases {
		if got[name] != want {
			t.Errorf("cppVisibility(%s) = %q, want %q", name, got[name], want)
		}
	}
}
