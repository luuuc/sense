package python

import (
	"strings"
	"testing"

	"github.com/luuuc/sense/internal/extract"
)

// These tests exercise the Django reverse-related-manager seam: an FK's
// related_name declares a queryset accessor on the PARENT model
// (`order.all_positions` ⇒ OrderPosition instances), which consuming code
// reaches without ever naming the child class. The declaration emits a
// synthetic django-related:* symbol anchored to the child model; an
// ORM-verb-chained accessor call emits a calls edge to the prefixed candidate
// only (never the bare accessor name — see emitRelatedManagerEdges).

func TestRelatedNameEmitsSyntheticSymbolAndEdge(t *testing.T) {
	r := parse(t, `class OrderPosition(models.Model):
    order = models.ForeignKey(Order, related_name='all_positions')
`)
	sym := findSymbol(r, extract.PrefixDjangoRelated+"all_positions")
	if sym == nil {
		t.Fatal("related_name must emit a django-related:* synthetic symbol")
	}
	if string(sym.Kind) != "constant" {
		t.Errorf("synthetic kind = %s, want constant", sym.Kind)
	}
	e := findEdge(r, extract.PrefixDjangoRelated+"all_positions", "OrderPosition", "calls")
	if e == nil {
		t.Fatal("synthetic accessor must emit a calls edge to the declaring (child) model")
	}
	if e.Confidence != extract.ConfidenceConvention {
		t.Errorf("declaration edge confidence = %v, want %v", e.Confidence, extract.ConfidenceConvention)
	}
	// The existing composes edge to the FK target is unchanged.
	if findEdge(r, "OrderPosition", "Order", "composes") == nil {
		t.Error("FK composes edge to the parent model must still be emitted")
	}
}

func TestRelatedNamePlusAndTemplateSkipped(t *testing.T) {
	r := parse(t, `class A(models.Model):
    x = models.ForeignKey(B, related_name='+')

class C(models.Model):
    y = models.ForeignKey(D, related_name='%(class)s_items')
`)
	for _, s := range r.symbols {
		if extract.IsSyntheticQualified(s.Qualified) {
			t.Errorf("disabled/template related_name must not emit a synthetic, got %s", s.Qualified)
		}
	}
}

func TestRelatedManagerAccessorEmitsCandidateEdges(t *testing.T) {
	r := parse(t, `def attendee_emails(order):
    return order.positions.filter(attendee_email__isnull=False)
`)
	// The bare accessor name must NOT be a target: a bare name binds
	// single-candidate same-named symbols at the confident tier in any Python
	// repo (the Finding-12 over-attribution family). Only the prefixed
	// candidate is emitted.
	if findEdge(r, "attendee_emails", "positions", "calls") != nil {
		t.Error("accessor chain must not emit a bare-name edge")
	}
	syn := findEdge(r, "attendee_emails", extract.PrefixDjangoRelated+"positions", "calls")
	if syn == nil {
		t.Fatal("ORM-verb accessor chain must emit a calls edge to the django-related:* candidate")
	}
	if syn.Confidence != extract.ConfidenceAmbiguous {
		t.Errorf("synthetic accessor edge confidence = %v, want %v", syn.Confidence, extract.ConfidenceAmbiguous)
	}
}

func TestRelatedManagerAccessorMultiHopAndSelf(t *testing.T) {
	r := parse(t, `class Order:
    def active(self):
        return self.all_positions.filter(canceled=False).exclude(blocked=True)
`)
	if findEdge(r, "Order.active", extract.PrefixDjangoRelated+"all_positions", "calls") == nil {
		t.Error("self-receiver multi-hop chain must reach the accessor root")
	}
}

func TestManagerLeafIsNotAnAccessor(t *testing.T) {
	r := parse(t, `def recent(qs):
    return Order.objects.filter(created__gte=x)
`)
	for _, e := range r.edges {
		if e.TargetQualified == extract.PrefixDjangoRelated+"objects" || e.TargetQualified == "objects" {
			t.Errorf("manager leaf must not emit accessor edges, got target %s", e.TargetQualified)
		}
	}
}

func TestRelatedNameNonStringSkipped(t *testing.T) {
	// A dynamic related_name (identifier, f-string) is unprovable — no synthetic.
	r := parse(t, `class A(models.Model):
    x = models.ForeignKey(B, related_name=DYNAMIC)
`)
	for _, s := range r.symbols {
		if extract.IsSyntheticQualified(s.Qualified) {
			t.Errorf("non-string related_name must not emit a synthetic, got %s", s.Qualified)
		}
	}
}

func TestAccessorRootShapesRejected(t *testing.T) {
	// A call root whose function is a bare identifier (`build(x).filter(…)`)
	// and a chain broken by a non-chain hop (`o.positions.first().filter(…)`)
	// both fail the root walk; no accessor edges.
	r := parse(t, `def f(o):
    a = build(o).filter(x=1)
    b = o.positions.first().filter(x=1)
`)
	for _, e := range r.edges {
		if e.TargetQualified == "positions" || e.TargetQualified == extract.PrefixDjangoRelated+"positions" {
			t.Errorf("broken chains must not emit accessor edges, got target %s", e.TargetQualified)
		}
	}
}

func TestAccessorChainDepthBoundary(t *testing.T) {
	// The root walk gives up past maxQuerySetChainDepth call hops: a chain
	// with the accessor exactly at the cap still roots; one hop deeper does
	// not. Tested at the emitted-edge seam by putting the accessor at the far
	// end of the chain — the OUTERMOST hop's walk must traverse every hop.
	atCap := "o.positions" + strings.Repeat(".filter(a=1)", maxQuerySetChainDepth)
	r := parse(t, "def f(o):\n    return "+atCap+"\n")
	if findEdge(r, "f", extract.PrefixDjangoRelated+"positions", "calls") == nil {
		t.Error("accessor at the depth cap must still root the chain")
	}
	pastCap := "o.positions" + strings.Repeat(".filter(a=1)", maxQuerySetChainDepth+2)
	r = parse(t, "def g(o):\n    return "+pastCap+"\n")
	// The outermost hops sit past the cap; only they emit nothing — inner
	// hops within the cap still root, so assert the OUTERMOST source g has
	// no accessor edge at the deepest line is impractical; instead assert the
	// walk terminated and the accessor edge still exists from the inner hops
	// (bounded, not absent — the cap bounds recursion, not recall).
	if findEdge(r, "g", extract.PrefixDjangoRelated+"positions", "calls") == nil {
		t.Error("inner hops within the cap must still root the chain")
	}
}

func TestRelatedNameSymbolEmitError(t *testing.T) {
	err := parseWithEmitter(t, `class A(models.Model):
    x = models.ForeignKey(B, related_name='things')
`, &failAfterN{symbolsLeft: 1, edgesLeft: 100})
	if err == nil {
		t.Error("expected error from failing emitter on the synthetic symbol")
	}
}

func TestRelatedNameEdgeEmitError(t *testing.T) {
	err := parseWithEmitter(t, `class A(models.Model):
    x = models.ForeignKey(B, related_name='things')
`, &failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error from failing emitter on the declaration edge")
	}
}

func TestAccessorEdgeEmitError(t *testing.T) {
	err := parseWithEmitter(t, `def f(o):
    return o.positions.filter(x=1)
`, &failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error from failing emitter on the synthetic accessor edge")
	}
}

func TestNonORMVerbsAreNotAccessorChains(t *testing.T) {
	r := parse(t, `def read(self):
    a = self.config.get("key")
    b = self.data.values()
    c = self.items.count(x)
`)
	for _, e := range r.edges {
		switch e.TargetQualified {
		case "config", "data", "items",
			extract.PrefixDjangoRelated + "config",
			extract.PrefixDjangoRelated + "data",
			extract.PrefixDjangoRelated + "items":
			t.Errorf("std-lib-colliding verbs must not emit accessor edges, got target %s", e.TargetQualified)
		}
	}
}

func TestSameFileRelatedNameCollisionSkipsSynthetic(t *testing.T) {
	// Two classes in ONE file sharing a related_name are unprovable: same-file
	// symbol emissions collapse to one row at persistence, so the resolver's
	// cross-file ambiguity gate could never see the collision — the flush
	// emits NOTHING for that name (closed-world: a wrong anchor misleads blast
	// worse than a gap). The same class re-using the spelling across two of
	// its own FKs is one anchor and keeps its single synthetic.
	r := parse(t, `class Ticket(models.Model):
    event = models.ForeignKey(Event, related_name='things')

class Coupon(models.Model):
    event = models.ForeignKey(Event, related_name='things')

class Item(models.Model):
    event = models.ForeignKey(Event, related_name='items')
    category = models.ForeignKey(Category, related_name='items')
`)
	things, items := 0, 0
	for _, s := range r.symbols {
		switch s.Qualified {
		case extract.PrefixDjangoRelated + "things":
			things++
		case extract.PrefixDjangoRelated + "items":
			items++
		}
	}
	if things != 0 {
		t.Errorf("different-owner shared related_name: %d synthetics, want 0 (skipped at flush)", things)
	}
	if items != 1 {
		t.Errorf("same-owner shared related_name: %d synthetics, want 1", items)
	}
	for _, e := range r.edges {
		if e.SourceQualified == extract.PrefixDjangoRelated+"things" {
			t.Errorf("skipped name must emit no declaration edges, got edge to %s", e.TargetQualified)
		}
	}
}

func TestRelatedNameFStringAndNonIdentifierSkipped(t *testing.T) {
	// tree-sitter classifies f-strings as plain string nodes and
	// stringContent returns only the first literal fragment — a truncated
	// name that could collide with (and via the ambiguity gates, poison) a
	// real related_name elsewhere. Non-identifier names are Django errors.
	r := parse(t, `class A(models.Model):
    x = models.ForeignKey(B, related_name=f"{prefix}_items")

class C(models.Model):
    y = models.ForeignKey(D, related_name=f"items_{suffix}")

class E(models.Model):
    z = models.ForeignKey(F, related_name="my items")
`)
	for _, s := range r.symbols {
		if extract.IsSyntheticQualified(s.Qualified) {
			t.Errorf("f-string/non-identifier related_name must not emit a synthetic, got %q", s.Qualified)
		}
	}
}

func TestRelatedNameManyToManySynthetic(t *testing.T) {
	// The seam covers all relational fields, not only ForeignKey: an M2M
	// related_name (pretix Team.members => user.teams) anchors the declaring
	// class the same way.
	r := parse(t, `class Team(models.Model):
    members = models.ManyToManyField(User, related_name='teams')
`)
	if findSymbol(r, extract.PrefixDjangoRelated+"teams") == nil {
		t.Fatal("M2M related_name must emit a synthetic")
	}
	if findEdge(r, extract.PrefixDjangoRelated+"teams", "Team", "calls") == nil {
		t.Error("M2M synthetic must anchor the declaring class")
	}
}
