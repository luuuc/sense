package python

import (
	"testing"

	"github.com/luuuc/sense/internal/extract"
)

// These tests exercise Celery async-dispatch edge resolution: a call to
// `task.delay(...)` / `task.apply_async(...)` emits a calls edge to the task
// function (the receiver), never to the dispatch method.

func TestCeleryDelayEmitsTaskEdge(t *testing.T) {
	r := parse(t, `def enqueue():
    send_email.delay("a@b.co")
`)
	e := findEdge(r, "enqueue", "send_email", "calls")
	if e == nil {
		t.Fatal("expected calls edge enqueue -> send_email for .delay dispatch")
	}
	if e.Confidence != 0.9 {
		t.Errorf("celery dispatch confidence = %v, want 0.9 (convention)", e.Confidence)
	}
	if findEdge(r, "enqueue", "delay", "calls") != nil {
		t.Error("must not emit an edge to the dispatch method `delay`")
	}
}

func TestCeleryApplyAsyncEmitsTaskEdge(t *testing.T) {
	r := parse(t, `def enqueue(order_id):
    process_payment.apply_async(args=[order_id])
`)
	if findEdge(r, "enqueue", "process_payment", "calls") == nil {
		t.Fatal("expected calls edge enqueue -> process_payment for .apply_async dispatch")
	}
	if findEdge(r, "enqueue", "apply_async", "calls") != nil {
		t.Error("must not emit an edge to the dispatch method `apply_async`")
	}
}

func TestCeleryDottedReceiverUsesLastSegment(t *testing.T) {
	// `tasks.cleanup_expired.delay()` — the task is the receiver's last segment.
	r := parse(t, `def enqueue():
    tasks.cleanup_expired.delay()
`)
	if findEdge(r, "enqueue", "cleanup_expired", "calls") == nil {
		t.Fatal("expected calls edge enqueue -> cleanup_expired for dotted-receiver dispatch")
	}
}

func TestCeleryNonDispatchMethodUnaffected(t *testing.T) {
	// `.run()` is not a dispatch method: the default attribute-call edge
	// (surface text) is emitted, no task-receiver edge at 0.9. The receiver
	// `worker` is a lowercase variable of unverified type, so the call is
	// emitted at ConfidenceUnresolved (not 1.0) — an unknown-receiver instance
	// call the resolver must not surface as a confident caller.
	r := parse(t, `def go():
    worker.run()
`)
	e := findEdge(r, "go", "worker.run", "calls")
	if e == nil {
		t.Fatal("expected the default attribute-call edge for a non-dispatch method")
	}
	if e.Confidence != extract.ConfidenceUnresolved {
		t.Errorf("default attribute-call confidence = %v, want %v (unverified receiver)", e.Confidence, extract.ConfidenceUnresolved)
	}
	if findEdge(r, "go", "worker", "calls") != nil {
		t.Error("non-dispatch `.run()` must not emit a task-receiver edge")
	}
}

func TestCeleryDynamicReceiverSkipped(t *testing.T) {
	// `task.s(x).apply_async()` — the receiver is a call, not a static task
	// reference; no convention edge is guessed.
	r := parse(t, `def enqueue(x):
    task.s(x).apply_async()
`)
	for _, e := range r.edges {
		if e.SourceQualified == "enqueue" && e.Confidence == 0.9 {
			t.Errorf("unexpected convention edge for dynamic receiver: -> %v", e.TargetQualified)
		}
	}
}
