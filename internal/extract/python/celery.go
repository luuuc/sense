package python

import (
	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
)

// celeryDispatchMethods are the Celery async-dispatch entrypoints. A call to
// one of these on a task object (`task.delay(...)`, `task.apply_async(...)`)
// ultimately runs that task's function body — an edge that does not exist in
// the greppable text, because the call names the dispatch method, not the
// task. Matching on the framework's dispatch API (not on a `*_task` name
// convention) is the structural signal that the receiver is a task: `.delay`
// and `.apply_async` are near-unique to Celery. This mirrors the Ruby
// Sidekiq/ActiveJob enqueue resolution (internal/extract/ruby/enqueue.go).
var celeryDispatchMethods = map[string]bool{
	"delay":       true,
	"apply_async": true,
}

// tryEmitCeleryDispatch handles a Celery task dispatch
// (`task.delay(...)`, `mod.task.apply_async(...)`) by emitting a calls edge
// from the enclosing symbol to the task function (the dispatch call's
// receiver) at convention confidence (0.9): the edge is inferred from the
// dispatch convention, never certain, so it is never stamped 1.0. The dispatch
// API is itself the structural task marker — no task-name convention.
//
// fn is the call's `function` node, already known to be an `attribute`. The
// receiver must be an identifier (`task`) or a dotted attribute (`mod.task`);
// a dynamic receiver (a call/subscript such as `task.s(x).apply_async()`)
// cannot be tied to a task statically, so no edge is emitted rather than a
// guessed one.
//
// Safety of the receiver guess is empirical, not structural, exactly as in the
// Ruby analog: the dispatch API names (delay/apply_async) are near-unique to
// Celery, so a `name.delay(...)` whose `name` is not a task is vanishingly
// rare. When it does happen and `name` resolves to an unrelated symbol, the
// edge points there at 0.9 — a wrong edge; when `name` resolves to nothing the
// edge simply drops at resolution. We accept that residual risk because the
// dispatch-API names make it negligible in practice.
//
// Returns handled=true when the call was a task dispatch, so the caller skips
// the (unresolvable `….apply_async`) default edge it would otherwise emit.
func (w *walker) tryEmitCeleryDispatch(fn *sitter.Node, source string, line int) (bool, error) {
	if !celeryDispatchMethods[attrLastSegment(fn, w.source)] {
		return false, nil
	}
	target := ""
	if recv := fn.ChildByFieldName("object"); recv != nil {
		switch recv.Kind() {
		case "identifier":
			target = extract.Text(recv, w.source)
		case "attribute":
			target = attrLastSegment(recv, w.source)
		}
	}
	if target == "" {
		return false, nil
	}
	return true, w.emit.Edge(extract.EmittedEdge{
		SourceQualified: source,
		TargetQualified: target,
		Kind:            model.EdgeCalls,
		Line:            &line,
		Confidence:      extract.ConfidenceConvention,
	})
}
