package php

import (
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
)

// Eloquent framework inference: relations, scopes,
// and observers - the model magic that links classes with no textual
// reference between them.
//
//   - `return $this->hasMany(OrderItem::class)` (any verb in
//     model.LaravelRelationVerbs) emits a composes edge Model → OrderItem
//     at convention confidence, the Rails-association parity edge.
//   - `public function scopeActive($q)` declares the callable `->active()`
//     Eloquent synthesizes via __call: the extractor emits an alias method
//     symbol `Model\active` plus a calls edge alias → scopeActive, so both
//     spellings resolve and the graph walks from the call-site name to the
//     real body.
//   - `#[ObservedBy(OrderObserver::class)]` (class or array form) emits a
//     calls edge Model → Observer; `Order::observe(OrderObserver::class)`
//     emits a calls edge from the registering site to the observer.

// emitRelation recognises `$this-><verb>(X::class, …)` inside a model
// method and emits the composes edge from the enclosing class. handled
// reports whether the call was consumed (the framework verb call itself is
// plumbing and emits nothing else).
func (w *walker) emitRelation(n *sitter.Node, name, class string) (bool, error) {
	if class == "" || !model.LaravelRelationVerbs[name] {
		return false, nil
	}
	obj := n.ChildByFieldName("object")
	if obj == nil || obj.Kind() != "variable_name" ||
		extract.Text(obj.NamedChild(0), w.source) != "this" {
		return false, nil
	}
	related := w.classConstant(argExpr(n, 0))
	if related == "" {
		return false, nil
	}
	line := extract.Line(n.StartPosition())
	return true, w.emit.Edge(extract.EmittedEdge{
		SourceQualified: class,
		TargetQualified: related,
		Kind:            model.EdgeComposes,
		Line:            &line,
		Confidence:      extract.ConfidenceConvention,
	})
}

// scopeAlias returns the call-site name a `scopeX` method declares
// (`scopeActive` → `active`), or "" for a non-scope method.
func scopeAlias(name string) string {
	rest, ok := strings.CutPrefix(name, "scope")
	if !ok || rest == "" {
		return ""
	}
	first := rune(rest[0])
	if first < 'A' || first > 'Z' {
		return ""
	}
	return strings.ToLower(rest[:1]) + rest[1:]
}

// emitScopeAlias emits the synthesized query-scope callable for a scopeX
// declaration: the alias method symbol plus its edge to the real body.
// Eloquent's __call only synthesizes the alias when the class does NOT
// define a real method of that name, so a declared method suppresses it.
func (w *walker) emitScopeAlias(n *sitter.Node, name, classQualified string, declared map[string]bool) error {
	alias := scopeAlias(name)
	if alias == "" || declared[alias] {
		return nil
	}
	line := extract.Line(n.StartPosition())
	if err := w.emit.Symbol(extract.EmittedSymbol{
		Name:            alias,
		Qualified:       classQualified + `\` + alias,
		Kind:            model.KindMethod,
		Visibility:      "public",
		Receiver:        extract.ReceiverInstance,
		ParentQualified: classQualified,
		LineStart:       line,
		LineEnd:         extract.Line(n.EndPosition()),
	}); err != nil {
		return err
	}
	return w.emit.Edge(extract.EmittedEdge{
		SourceQualified: classQualified + `\` + alias,
		TargetQualified: classQualified + `\` + name,
		Kind:            model.EdgeCalls,
		Line:            &line,
		Confidence:      extract.ConfidenceConvention,
	})
}

// emitObservedBy reads a class's #[ObservedBy(...)] attribute (single
// class constant or array form) and emits a calls edge to each observer.
func (w *walker) emitObservedBy(n *sitter.Node, qualified string) error {
	attrs := firstChildKind(n, "attribute_list")
	if attrs == nil {
		return nil
	}
	var observers []string
	_ = extract.WalkNamedDescendants(attrs, "attribute", func(attr *sitter.Node) error {
		// The attribute's name child carries no field label in the grammar.
		if extract.Text(firstChildKind(attr, "name", "qualified_name"), w.source) != "ObservedBy" {
			return nil
		}
		args := firstChildKind(attr, "arguments")
		if args == nil {
			return nil
		}
		for i := uint(0); i < args.NamedChildCount(); i++ {
			a := args.NamedChild(i)
			if a == nil || a.Kind() != "argument" {
				continue
			}
			observers = append(observers, w.listenerClasses(a.NamedChild(0))...)
		}
		return nil
	})
	line := extract.Line(n.StartPosition())
	for _, obs := range observers {
		if err := w.emit.Edge(extract.EmittedEdge{
			SourceQualified: qualified,
			TargetQualified: obs,
			Kind:            model.EdgeCalls,
			Line:            &line,
			Confidence:      extract.ConfidenceConvention,
		}); err != nil {
			return err
		}
	}
	return nil
}

// emitObserveCall handles `Order::observe(OrderObserver::class)`: a calls
// edge from the registering site to the observer class.
func (w *walker) emitObserveCall(n *sitter.Node, src string) error {
	if observer := w.classConstant(argExpr(n, 0)); observer != "" {
		return w.callEdge(n, src, observer, extract.ConfidenceConvention)
	}
	return nil
}
