package ruby

import (
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
)

// enqueueMethods are the Sidekiq / ActiveJob enqueue entrypoints. A call to
// one of these on a worker/job class ultimately runs that class's #perform —
// an edge that does not exist in the greppable text, because the call names
// the enqueue method, not #perform. Matching on the framework's enqueue API
// (not on a `*Worker` name convention) is the structural signal that the
// receiver is a worker: only a Sidekiq worker responds to perform_async /
// push_bulk, only an ActiveJob job to perform_later.
var enqueueMethods = map[string]bool{
	// Sidekiq
	"perform_async": true,
	"perform_in":    true,
	"perform_at":    true,
	"perform_bulk":  true,
	"push_bulk":     true,
	// ActiveJob
	"perform_later": true,
}

// enqueueTarget returns the worker run-method target (`Receiver#perform`) for
// an enqueue call, or "" when the call is not an enqueue of a constant-named
// worker. The receiver must be a constant or scope_resolution
// (`DeliveryWorker`, `ActivityPub::DeliveryWorker`); a dynamic receiver
// (variable, self, or receiverless) cannot be tied to a worker class
// statically, so no edge is emitted rather than a guessed one.
func enqueueTarget(n *sitter.Node, methodName string, source []byte) string {
	if !enqueueMethods[methodName] {
		return ""
	}
	recv := n.ChildByFieldName("receiver")
	if recv == nil {
		return ""
	}
	switch recv.Kind() {
	case "constant", "scope_resolution":
		// A constant/scope_resolution node always carries source text, so the
		// trimmed receiver is non-empty by construction. Every method in
		// enqueueMethods runs the worker's `#perform`; if a future entry runs a
		// differently-named method, this target must become table-driven.
		return strings.TrimSpace(extract.Text(recv, source)) + "#perform"
	}
	return ""
}

// tryEmitEnqueueEdge handles a Sidekiq/ActiveJob enqueue call
// (`Worker.perform_async(...)`, `Worker.push_bulk(...) do ... end`,
// `Job.perform_later(...)`) by emitting a calls edge from the enclosing method
// to the worker's run method `Worker#perform` at convention confidence (0.9):
// the edge is inferred from the enqueue convention, never certain, so it is
// never stamped 1.0. The enqueue API is itself the structural worker/job
// marker — no `*Worker` name match.
//
// Safety of the receiver guess is empirical, not structural: the enqueue API
// names (perform_async/push_bulk/perform_later/…) are near-unique to Sidekiq/
// ActiveJob, so a `Const.perform_async` whose `Const` is *not* a worker is
// vanishingly rare. When it does happen and `Const` defines (or inherits) an
// unrelated `#perform`, the edge resolves to it at 0.9 — a wrong edge. We
// accept that residual risk because the enqueue-API names make it negligible in
// practice; a receiver with no `#perform` anywhere simply drops at resolution.
//
// Returns handled=true when the call was an enqueue of a constant-named
// worker, so the caller skips the (unresolvable) `Worker.perform_async`
// target edge it would otherwise emit. A block argument (push_bulk's
// `do |x| ... end`) is still walked so real calls inside it — e.g.
// `build_json(follow)` in suspend_account_service — are not lost.
//
// methodName is the already-extracted call method name (both callers extract
// it before dispatching), passed in to avoid re-reading the method node.
func (w *walker) tryEmitEnqueueEdge(n *sitter.Node, methodName, source string, scope []string, localTypes, ivarTypes map[string]string) (bool, error) {
	target := enqueueTarget(n, methodName, w.source)
	if target == "" {
		return false, nil
	}
	line := extract.Line(n.StartPosition())
	if err := w.emit.Edge(extract.EmittedEdge{
		SourceQualified: source,
		TargetQualified: target,
		Kind:            model.EdgeCalls,
		Line:            &line,
		Confidence:      extract.ConfidenceConvention,
	}); err != nil {
		return true, err
	}
	return true, w.walkBlockCalls(n, source, scope, localTypes, ivarTypes)
}
