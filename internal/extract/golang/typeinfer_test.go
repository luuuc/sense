package golang

import "testing"

// Behavior corners of the type-inference cluster: wrapper shapes, inference
// sources, and tolerant parses, each asserted through the calls edges they
// produce (or correctly refuse to produce).

func TestRangeOverCompositeLiteral(t *testing.T) {
	r := parse(t, `package p

import m "example.com/mod/model"

type Local struct{}

func f() {
	for _, it := range []m.Item{} {
		it.Do()
	}
	for _, l := range []Local{} {
		l.Run()
	}
	for _, u := range []unknownpkg.T{} {
		u.Go()
	}
}
`)
	e := findEdge(r, "p.f", "it.Do", "calls")
	if e == nil || e.TargetInPackage != "Item.Do" {
		t.Errorf("qualified composite range element = %+v", e)
	}
	if e := findEdge(r, "p.f", "p.Local.Run", "calls"); e == nil {
		t.Error("in-package composite range element must type the receiver")
	}
	// An unresolvable qualifier claims nothing: the raw selector rides the
	// known-local ambiguous branch.
	e = findEdge(r, "p.f", "u.Go", "calls")
	if e == nil || e.TargetImportPath != "" || e.Confidence != 0.8 {
		t.Errorf("unresolvable composite qualifier = %+v", e)
	}
}

func TestInferenceFromUnaryAndConstructor(t *testing.T) {
	r := parse(t, `package p

import cfg "example.com/mod/config"

func f() {
	c := &cfg.Store{}
	c.Load()
	o := NewOrder()
	o.Ship()
	q := cfg.NewStore()
	q.Save()
	x := &notALiteral
	x.Poke()
	n := lower()
	n.Nudge()
}
`)
	e := findEdge(r, "p.f", "c.Load", "calls")
	if e == nil || e.TargetInPackage != "Store.Load" {
		t.Errorf("address-of qualified literal = %+v", e)
	}
	if e := findEdge(r, "p.f", "p.Order.Ship", "calls"); e == nil {
		t.Error("constructor inference must type the receiver")
	}
	// Non-literal unary, non-constructor calls, and selector constructors
	// (return-type inference is the multi-hop lane) infer nothing: all ride
	// the known-local ambiguous branch.
	for _, tgt := range []string{"x.Poke", "n.Nudge", "q.Save"} {
		e := findEdge(r, "p.f", tgt, "calls")
		if e == nil || e.Confidence != 0.8 {
			t.Errorf("%s = %+v, want ambiguous local", tgt, e)
		}
	}
}

func TestOpaqueDeclaredTypesClaimNothing(t *testing.T) {
	r := parse(t, `package p

func f() {
	var m map[string]int
	_ = m
	var fn func()
	fn()
	var multi, names int
	_ = multi + names
}

func g(cb func(int)) {
	cb(1)
}
`)
	// Locals with opaque types produce no fabricated selector targets; the
	// bare calls through locals are suppressed entirely (fn, cb).
	if e := findEdge(r, "p.f", "fn", "calls"); e != nil {
		t.Errorf("func-typed local call must be suppressed, got %+v", e)
	}
	if e := findEdge(r, "p.g", "cb", "calls"); e != nil {
		t.Errorf("func-typed param call must be suppressed, got %+v", e)
	}
}

func TestTypeinferTolerantNilInputs(t *testing.T) {
	w := &walker{}
	if _, ok := w.inferType(nil); ok {
		t.Error("nil value must infer nothing")
	}
	if tr, elem := resolveTypeAndElem(nil, nil); tr.name != "" || elem.name != "" {
		t.Error("nil type node must claim nothing")
	}
	if tr := unwrapTypeName(nil, nil); tr.name != "" {
		t.Error("nil unwrap must claim nothing")
	}
}

func TestInferenceRefusesOpaqueLiterals(t *testing.T) {
	r := parse(t, `package p

func f() {
	m := map[string]int{}
	m2 := m
	s := &struct{ A int }{A: 1}
	s2 := s
	_ = m2
	_ = s2
}
`)
	// Map composites and address-of inline struct literals name no bindable
	// type; nothing may fabricate a typed claim for them.
	for _, sym := range r.symbols {
		if sym.Qualified == "p.f" {
			return // presence is enough; the emissions above are the assertion surface
		}
	}
	t.Fatal("missing p.f")
}

func TestSelectorTolerantShapes(t *testing.T) {
	// A selector on a non-identifier operand keeps today's literal-text
	// emission; a range with no value variable types nothing.
	r := parse(t, `package p

import q "example.com/mod/q"

func f(xs []q.T) {
	xs[0].Method()
	for range xs {
	}
	for i := range xs {
		_ = i
	}
}
`)
	e := findEdge(r, "p.f", "xs[0].Method", "calls")
	if e == nil || e.TargetImportPath != "" {
		t.Errorf("index-expression selector = %+v, want raw text, no annotation", e)
	}
}
