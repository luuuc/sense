package ruby

import (
	"testing"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
)

func superEdge(r *recorder, sourceMethod, target string) *extract.EmittedEdge {
	return findEdge(r, sourceMethod, target, string(model.EdgeCalls))
}

func TestSuperBareEmitsParentMethodEdge(t *testing.T) {
	src := `class Sub < Base
  def perform(x)
    setup
    super
  end
end`
	r := parseRuby(t, src)
	e := superEdge(r, "Sub#perform", "Base#perform")
	if e == nil {
		t.Fatal("bare super should emit a calls edge to Base#perform")
	}
	if e.Confidence != extract.ConfidenceConvention {
		t.Errorf("super edge confidence = %v, want %v", e.Confidence, extract.ConfidenceConvention)
	}
}

func TestSuperWithArgsEmitsParentMethodEdge(t *testing.T) {
	src := `class Sub < Base
  def perform(json, id)
    super(json, id)
  end
end`
	r := parseRuby(t, src)
	if superEdge(r, "Sub#perform", "Base#perform") == nil {
		t.Fatal("super(args) should emit a calls edge to Base#perform")
	}
}

func TestSuperNamespacedSuperclass(t *testing.T) {
	// Mirrors mastodon: CollectionRawDistributionWorker < ActivityPub::RawDistributionWorker.
	src := `class ActivityPub::CollectionRawDistributionWorker < ActivityPub::RawDistributionWorker
  def perform(json, collection_id)
    super(json, account_id)
  end
end`
	r := parseRuby(t, src)
	if superEdge(r, "ActivityPub::CollectionRawDistributionWorker#perform", "ActivityPub::RawDistributionWorker#perform") == nil {
		t.Fatal("super should target the fully-qualified superclass run method")
	}
}

func TestSuperOnlyOneEdgePerMethod(t *testing.T) {
	src := `class Sub < Base
  def perform
    super
    super()
  end
end`
	r := parseRuby(t, src)
	count := 0
	for _, e := range r.edges {
		if e.SourceQualified == "Sub#perform" && e.TargetQualified == "Base#perform" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly one super edge per method, got %d", count)
	}
}

func TestSuperSingletonUsesDotSeparator(t *testing.T) {
	src := `class Sub < Base
  def self.build(x)
    super
  end
end`
	r := parseRuby(t, src)
	if superEdge(r, "Sub.build", "Base.build") == nil {
		t.Fatal("super in a singleton method should target Base.build (dot separator)")
	}
}

func TestSuperNoSuperclassNoEdge(t *testing.T) {
	// A class with no superclass: `super` would be a runtime NoMethodError, but
	// we must emit no guessed edge.
	src := `class Standalone
  def perform
    super
  end
end`
	r := parseRuby(t, src)
	for _, e := range r.edges {
		if e.SourceQualified == "Standalone#perform" && e.TargetQualified == "#perform" {
			t.Error("a class with no superclass must emit no super edge")
		}
	}
}

func TestSuperNotAttributedAcrossNestedDef(t *testing.T) {
	// A `super` inside a nested method definition belongs to that nested method,
	// not the outer one. The outer method (no super of its own) must emit no
	// super edge.
	src := `class Sub < Base
  def perform
    define_singleton_method(:inner) do
    end

    def helper
      super
    end
  end
end`
	r := parseRuby(t, src)
	if superEdge(r, "Sub#perform", "Base#perform") != nil {
		t.Error("outer #perform has no super of its own; the nested def's super must not be attributed to it")
	}
}

func TestSuperAbsentNoEdge(t *testing.T) {
	src := `class Sub < Base
  def perform
    do_work
  end
end`
	r := parseRuby(t, src)
	if superEdge(r, "Sub#perform", "Base#perform") != nil {
		t.Error("a method without super must emit no super edge")
	}
}
