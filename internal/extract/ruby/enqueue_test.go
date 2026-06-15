package ruby

import (
	"testing"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
)

// enqueueEdge finds the calls edge a `*.perform_async`-style call should emit
// from sourceMethod to `<worker>#perform`.
func enqueueEdge(r *recorder, sourceMethod, worker string) *extract.EmittedEdge {
	return findEdge(r, sourceMethod, worker+"#perform", string(model.EdgeCalls))
}

func TestEnqueuePerformAsyncEmitsRunMethodEdge(t *testing.T) {
	src := `class FollowService
  def call
    DeliveryWorker.perform_async(payload)
  end
end`
	r := parseRuby(t, src)
	e := enqueueEdge(r, "FollowService#call", "DeliveryWorker")
	if e == nil {
		t.Fatal("perform_async should emit a calls edge to DeliveryWorker#perform")
	}
	if e.Confidence != extract.ConfidenceConvention {
		t.Errorf("enqueue edge confidence = %v, want %v (inferred, never 1.0)", e.Confidence, extract.ConfidenceConvention)
	}
	// The unresolvable Worker.perform_async target must NOT also be emitted.
	if findEdge(r, "FollowService#call", "DeliveryWorker.perform_async", string(model.EdgeCalls)) != nil {
		t.Error("the literal Worker.perform_async target edge should be suppressed")
	}
}

func TestEnqueueNamespacedReceiver(t *testing.T) {
	src := `module ActivityPub
  class RawDistributionWorker
    def distribute!
      ActivityPub::DeliveryWorker.push_bulk(inboxes) do |inbox_url|
        [payload, inbox_url]
      end
    end
  end
end`
	r := parseRuby(t, src)
	if enqueueEdge(r, "ActivityPub::RawDistributionWorker#distribute!", "ActivityPub::DeliveryWorker") == nil {
		t.Fatal("push_bulk on a namespaced worker should emit edge to ActivityPub::DeliveryWorker#perform")
	}
}

func TestEnqueueActiveJobPerformLater(t *testing.T) {
	src := `class Mailer
  def deliver
    NotificationJob.perform_later(user_id)
  end
end`
	r := parseRuby(t, src)
	if enqueueEdge(r, "Mailer#deliver", "NotificationJob") == nil {
		t.Fatal("perform_later should emit a calls edge to NotificationJob#perform")
	}
}

func TestEnqueueAllSidekiqVariants(t *testing.T) {
	for _, m := range []string{"perform_async", "perform_in", "perform_at", "perform_bulk", "push_bulk"} {
		src := `class S
  def go
    MyWorker.` + m + `(arg)
  end
end`
		r := parseRuby(t, src)
		if enqueueEdge(r, "S#go", "MyWorker") == nil {
			t.Errorf("%s should emit a calls edge to MyWorker#perform", m)
		}
	}
}

func TestEnqueuePushBulkBlockCallsStillEmitted(t *testing.T) {
	// suspend_account_service's `push_bulk(follows) do |follow| [build_json(follow)] end`
	// — the enqueue edge AND the nested build_json call edge must both survive.
	src := `class SuspendAccountService
  def call
    DeliveryWorker.push_bulk(follows) do |follow|
      [build_json(follow), follow.account_id]
    end
  end

  def build_json(follow)
  end
end`
	r := parseRuby(t, src)
	if enqueueEdge(r, "SuspendAccountService#call", "DeliveryWorker") == nil {
		t.Error("push_bulk enqueue edge missing")
	}
	// build_json is a bare/self call inside the block; it resolves to self.build_json.
	if findEdge(r, "SuspendAccountService#call", "self.build_json", string(model.EdgeCalls)) == nil {
		t.Error("nested call inside the push_bulk block was lost")
	}
}

func TestEnqueueNonEnqueueCallNoPerformEdge(t *testing.T) {
	// A plain class-method call must NOT be rewritten to a #perform edge.
	src := `class C
  def go
    Math.sqrt(2)
    Foo.bar(x)
  end
end`
	r := parseRuby(t, src)
	if findEdge(r, "C#go", "Math#perform", string(model.EdgeCalls)) != nil {
		t.Error("Math.sqrt must not emit a #perform edge")
	}
	if findEdge(r, "C#go", "Foo#perform", string(model.EdgeCalls)) != nil {
		t.Error("Foo.bar must not emit a #perform edge")
	}
}

func TestEnqueueBareReceiverSkipped(t *testing.T) {
	// A receiverless enqueue call (`perform_async` with no constant receiver,
	// e.g. inside the worker calling itself) cannot name a worker class — no
	// edge.
	src := `class C
  def go
    perform_async(arg)
  end
end`
	r := parseRuby(t, src)
	for _, e := range r.edges {
		if e.TargetQualified == "#perform" || e.TargetQualified == "self#perform" {
			t.Errorf("bare perform_async should emit no enqueue edge, got %s", e.TargetQualified)
		}
	}
}

func TestEnqueueEmitErrorPropagates(t *testing.T) {
	// A failing edge emit inside the enqueue path must propagate out, not be
	// swallowed.
	err := parseWithEmitter(t, "class C\n  def go\n    DeliveryWorker.perform_async(x)\n  end\nend",
		&failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected the failing enqueue edge emit to propagate")
	}
}

func TestEnqueueDynamicReceiverSkipped(t *testing.T) {
	// A variable receiver cannot be tied to a worker class statically — emit
	// no guessed edge rather than a wrong one.
	src := `class C
  def go(klass)
    worker = klass
    worker.perform_async(arg)
  end
end`
	r := parseRuby(t, src)
	for _, e := range r.edges {
		if e.SourceQualified == "C#go" && len(e.TargetQualified) > 8 && e.TargetQualified[len(e.TargetQualified)-8:] == "#perform" {
			t.Errorf("dynamic receiver should emit no enqueue edge, got %s", e.TargetQualified)
		}
	}
}
