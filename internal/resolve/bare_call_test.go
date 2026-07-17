package resolve_test

import (
	"testing"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/resolve"
)

// bareCallRefs builds a minimal index for the bare-call grammar law: in Go and
// Rust a bare identifier call can never be a method call, so a bare target
// must never bind a method-kind symbol (G-10: pebble versionSet.append rode
// builtin `append(...)` calls to 778 fabricated verified-band edges).
//
// File 10 is a Go production file, file 12 a Rust one, file 11 a Ruby one
// (where bare calls ARE implicit-self method dispatch and must keep binding).
func bareCallRefs() []model.SymbolRef {
	return []model.SymbolRef{
		{ID: 1, Qualified: "pebble.versionSet", FileID: 10, Language: "go", Path: "version_set.go", Kind: model.KindType},
		{ID: 2, Qualified: "pebble.versionSet.append", FileID: 10, Language: "go", Path: "version_set.go", Kind: model.KindMethod},
		{ID: 3, Qualified: "Greeter#hello", FileID: 11, Language: "ruby", Path: "greeter.rb", Kind: model.KindMethod},
		{ID: 4, Qualified: "Buffer::len", FileID: 12, Language: "rust", Path: "buffer.rs", Kind: model.KindMethod},
	}
}

// The defect itself: a bare Go call site (`append(x, y)`) emitted at static
// confidence must NOT bind the same-named method. Today it resolves at 0.8.
func TestResolveGoBareCallNeverBindsMethod(t *testing.T) {
	ix := resolve.NewIndex(bareCallRefs())
	_, ok := ix.Resolve(resolve.Request{
		Target:         "append",
		Kind:           model.EdgeCalls,
		SourceFileID:   10,
		BaseConfidence: 1.0,
	})
	if ok {
		t.Fatal("bare Go call bound a method symbol — illegal by Go grammar (methods need a receiver expression)")
	}
}

// The positive Rust fixture the council required: the gate branch must be
// observed firing, not assumed from grammar. Same law, same shape.
func TestResolveRustBareCallNeverBindsMethod(t *testing.T) {
	ix := resolve.NewIndex(bareCallRefs())
	_, ok := ix.Resolve(resolve.Request{
		Target:         "len",
		Kind:           model.EdgeCalls,
		SourceFileID:   12,
		BaseConfidence: 1.0,
	})
	if ok {
		t.Fatal("bare Rust call bound a method symbol — illegal by Rust grammar (methods need a receiver or path)")
	}
}

// Mutation guard: Ruby bare calls are implicit-self method dispatch — the
// language gate must not leak. Kills the "drop the language gate" mutant.
func TestResolveRubyBareCallStillBindsMethod(t *testing.T) {
	ix := resolve.NewIndex(bareCallRefs())
	r, ok := ix.Resolve(resolve.Request{
		Target:         "hello",
		Kind:           model.EdgeCalls,
		SourceFileID:   11,
		BaseConfidence: 1.0,
	})
	if !ok {
		t.Fatal("Ruby bare call must keep binding methods (implicit self is real dispatch)")
	}
	if r.SymbolID != 3 {
		t.Errorf("SymbolID = %d, want 3", r.SymbolID)
	}
}

// Mutation guard: a bare Go call to an indexed FUNCTION is the legitimate
// case the fallback exists for. Kills the "drop all bare Go calls" mutant.
func TestResolveGoBareCallKeepsFunctionBind(t *testing.T) {
	rs := append(bareCallRefs(),
		model.SymbolRef{ID: 5, Qualified: "pebble.makeRoom", FileID: 10, Language: "go", Path: "version_set.go", Kind: model.KindFunction},
	)
	ix := resolve.NewIndex(rs)
	r, ok := ix.Resolve(resolve.Request{
		Target:         "makeRoom",
		Kind:           model.EdgeCalls,
		SourceFileID:   10,
		BaseConfidence: 1.0,
	})
	if !ok {
		t.Fatal("bare Go call to an indexed function must resolve")
	}
	if r.SymbolID != 5 {
		t.Errorf("SymbolID = %d, want 5", r.SymbolID)
	}
	if r.Confidence != 0.8 {
		t.Errorf("Confidence = %v, want 0.8 (unqualified-fallback clamp)", r.Confidence)
	}
}

// The promotion side effect, pinned on purpose (council: the fix PROMOTES as
// well as deletes). Today {function, method} under one bare name is an
// ambiguous set demoted below blast's floor; dropping the illegal method
// leaves a unique function candidate at the fallback clamp. That is a
// true-positive gain: the function was the only legal callee.
func TestResolveGoBareCallMixedSetPromotesFunction(t *testing.T) {
	rs := append(bareCallRefs(),
		model.SymbolRef{ID: 5, Qualified: "pebble.run", FileID: 10, Language: "go", Path: "version_set.go", Kind: model.KindFunction},
		model.SymbolRef{ID: 6, Qualified: "pebble.compaction.run", FileID: 13, Language: "go", Path: "compaction.go", Kind: model.KindMethod},
	)
	ix := resolve.NewIndex(rs)
	r, ok := ix.Resolve(resolve.Request{
		Target:         "run",
		Kind:           model.EdgeCalls,
		SourceFileID:   10,
		BaseConfidence: 1.0,
	})
	if !ok {
		t.Fatal("bare Go call must resolve to the function once the method is excluded")
	}
	if r.SymbolID != 5 {
		t.Errorf("SymbolID = %d, want 5 (the function, not the method)", r.SymbolID)
	}
	if r.Confidence != 0.8 {
		t.Errorf("Confidence = %v, want 0.8 (unique after the drop — promoted from the collision demotion)", r.Confidence)
	}
	if r.Ambiguous {
		t.Error("Ambiguous = true, want false (the method was never a legal candidate)")
	}
}

// Mutation guard: a DOTTED Go target leaf-falling to a method must stay
// resolvable — this band carries real reach (gitea context.Base's embedded
// dispatch edges). Kills the "drop the sep gate" mutant, which would wipe it.
func TestResolveGoDottedLeafStillBindsMethod(t *testing.T) {
	ix := resolve.NewIndex(bareCallRefs())
	r, ok := ix.Resolve(resolve.Request{
		Target:         "vs.append",
		Kind:           model.EdgeCalls,
		SourceFileID:   10,
		BaseConfidence: 1.0,
	})
	if !ok {
		t.Fatal("dotted Go target must keep leaf-falling to the method (receiver-unknown band)")
	}
	if r.SymbolID != 2 {
		t.Errorf("SymbolID = %d, want 2", r.SymbolID)
	}
}

// phpBareRefs models F-3: PHP has no implicit-self method dispatch, so a bare
// `count(...)` call is a builtin/function call and must never bind the
// same-named method - the exact shape that bound `Webkul\Core\Eloquent\
// Repository::count` to 14 files whose only tie was a PHP builtin `count()`.
// File 30 is a PHP production file.
func phpBareRefs() []model.SymbolRef {
	return []model.SymbolRef{
		{ID: 1, Qualified: `App\Repository`, FileID: 30, Language: "php", Path: "Repository.php", Kind: model.KindType},
		{ID: 2, Qualified: `App\Repository\count`, FileID: 30, Language: "php", Path: "Repository.php", Kind: model.KindMethod},
	}
}

// The defect itself (F-3): a bare PHP call `count(...)` emitted at static
// confidence must NOT bind the same-named method - PHP method dispatch requires
// a receiver (`$x->count()`), so a receiverless `count(` is a builtin/function.
func TestResolvePHPBareFunctionCallNeverBindsMethod(t *testing.T) {
	ix := resolve.NewIndex(phpBareRefs())
	_, ok := ix.Resolve(resolve.Request{
		Target:         "count",
		Kind:           model.EdgeCalls,
		SourceFileID:   30,
		BaseConfidence: extract.ConfidenceStatic,
	})
	if ok {
		t.Fatal("bare PHP call bound a method symbol - illegal by PHP grammar (no implicit-self dispatch; count() is a builtin/function)")
	}
}

// Mutation guard: a bare PHP call to an indexed FUNCTION is the legitimate case
// the fallback exists for (a Laravel global helper like `collect()`). Kills the
// "drop all bare PHP calls" over-fix mutant.
func TestResolvePHPBareCallKeepsFunctionBind(t *testing.T) {
	rs := append(phpBareRefs(),
		model.SymbolRef{ID: 5, Qualified: `App\Support\collect`, FileID: 30, Language: "php", Path: "helpers.php", Kind: model.KindFunction},
	)
	ix := resolve.NewIndex(rs)
	r, ok := ix.Resolve(resolve.Request{
		Target:         "collect",
		Kind:           model.EdgeCalls,
		SourceFileID:   30,
		BaseConfidence: extract.ConfidenceStatic,
	})
	if !ok {
		t.Fatal("bare PHP call to an indexed function must resolve")
	}
	if r.SymbolID != 5 {
		t.Errorf("SymbolID = %d, want 5 (the function)", r.SymbolID)
	}
}

// Mutation guard: PHP's unknown-receiver member fallback (decision 0003 clause
// 1) emits the bare method name at ConfidenceNameCollision - a genuine dispatch
// that must KEEP binding the method, demoted below blast's floor for liveness.
// The gate is confidence-aware for PHP: only ABOVE the collision band (a
// function call) are methods dropped. Kills the "put PHP in the Go/Rust camp"
// over-fix that would wipe this liveness edge.
func TestResolvePHPMemberFallbackKeepsMethodDemoted(t *testing.T) {
	ix := resolve.NewIndex(phpBareRefs())
	r, ok := ix.Resolve(resolve.Request{
		Target:         "count",
		Kind:           model.EdgeCalls,
		SourceFileID:   30,
		BaseConfidence: extract.ConfidenceNameCollision,
	})
	if !ok {
		t.Fatal("PHP member fallback at the collision band must keep binding the method (decision 0003 clause 1 liveness)")
	}
	if r.SymbolID != 2 {
		t.Errorf("SymbolID = %d, want 2 (the method)", r.SymbolID)
	}
	if r.Confidence >= extract.ConfidenceUnresolved {
		t.Errorf("Confidence = %v, want below blast's floor (a demoted guess)", r.Confidence)
	}
}

// Fail-open: an unknown source language must not drop method candidates —
// the gate acts only on languages whose grammar proves the bind illegal.
func TestResolveBareCallUnknownLanguageKeepsMethod(t *testing.T) {
	rs := []model.SymbolRef{
		{ID: 1, Qualified: "Widget.render", FileID: 40, Kind: model.KindMethod},
		{ID: 2, Qualified: "caller", FileID: 40},
	}
	ix := resolve.NewIndex(rs)
	r, ok := ix.Resolve(resolve.Request{
		Target:         "render",
		Kind:           model.EdgeCalls,
		SourceFileID:   40,
		BaseConfidence: 1.0,
	})
	if !ok {
		t.Fatal("unknown-language bare call must keep the method bind (fail-open)")
	}
	if r.SymbolID != 1 {
		t.Errorf("SymbolID = %d, want 1", r.SymbolID)
	}
}

// Mutation guard: a bare Go call to a TYPE (a conversion, `versionSet(x)`
// shape) must stay resolvable — the drop is methods-only, never a
// keep-functions whitelist. Kills the whitelist mutant that survives the
// rest of the suite (types and constants are legal bare callees).
func TestResolveGoBareCallKeepsTypeBind(t *testing.T) {
	ix := resolve.NewIndex(bareCallRefs())
	r, ok := ix.Resolve(resolve.Request{
		Target:         "versionSet",
		Kind:           model.EdgeCalls,
		SourceFileID:   10,
		BaseConfidence: 1.0,
	})
	if !ok {
		t.Fatal("bare Go call to an indexed type (conversion) must resolve")
	}
	if r.SymbolID != 1 {
		t.Errorf("SymbolID = %d, want 1", r.SymbolID)
	}
}
