package resolve_test

import (
	"testing"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/resolve"
)

// phpRefs models a Laravel app slice: a service with a charge method, a
// facade proxying it (ancestry carries the extract-side accessor edge), a
// base/sub pair for plain inheritance, and a PHP consumer file (ID 9) the
// requests originate from.
func phpRefs() []model.SymbolRef {
	return []model.SymbolRef{
		{ID: 1, Qualified: `App\Services\PaymentService`, FileID: 1, Language: "php"},
		{ID: 2, Qualified: `App\Services\PaymentService\charge`, FileID: 1, Language: "php"},
		{ID: 3, Qualified: `App\Facades\Payments`, FileID: 2, Language: "php"},
		{ID: 4, Qualified: `App\Base`, FileID: 3, Language: "php"},
		{ID: 5, Qualified: `App\Base\boot`, FileID: 3, Language: "php"},
		{ID: 6, Qualified: `App\Sub`, FileID: 4, Language: "php"},
		{ID: 7, Qualified: `App\Consumer`, FileID: 9, Language: "php"},
	}
}

func phpAncestry() map[string][]string {
	return map[string][]string{
		// The facade's ancestry holds BOTH the vendor extends target (never
		// indexed) and the extract-side proxy-IS-A accessor edge.
		`App\Facades\Payments`: {`Illuminate\Support\Facades\Facade`, `App\Services\PaymentService`},
		`App\Sub`:              {`App\Base`},
	}
}

func TestPHPFacadeCallBindsAccessorMethod(t *testing.T) {
	ix := resolve.NewIndex(phpRefs()).WithInheritance(phpAncestry())
	r, ok := ix.Resolve(resolve.Request{
		Target:         `App\Facades\Payments\charge`,
		Kind:           model.EdgeCalls,
		SourceFileID:   9,
		BaseConfidence: extract.ConfidenceStatic,
	})
	if !ok {
		t.Fatal("expected facade call to bind the accessor's method")
	}
	if r.SymbolID != 2 {
		t.Errorf("SymbolID = %d, want 2 (PaymentService\\charge)", r.SymbolID)
	}
	if r.Confidence != extract.ConfidenceStatic {
		t.Errorf("Confidence = %v, want base (unique ancestor keeps it)", r.Confidence)
	}
}

func TestPHPInheritedMethodResolves(t *testing.T) {
	ix := resolve.NewIndex(phpRefs()).WithInheritance(phpAncestry())
	r, ok := ix.Resolve(resolve.Request{
		Target:         `App\Sub\boot`,
		Kind:           model.EdgeCalls,
		SourceFileID:   9,
		BaseConfidence: extract.ConfidenceStatic,
	})
	if !ok {
		t.Fatal("expected inherited resolution to App\\Base\\boot")
	}
	if r.SymbolID != 5 {
		t.Errorf("SymbolID = %d, want 5", r.SymbolID)
	}
	if r.Confidence != extract.ConfidenceStatic {
		t.Errorf("Confidence = %v, want base", r.Confidence)
	}
}

func TestPHPInheritedLaneGatedOnSourceLanguage(t *testing.T) {
	// The same target from a non-PHP source file must not walk PHP ancestry
	// at full confidence: the `\` dispatch contract is PHP's alone. (The
	// leaf fallback may still produce a demoted cross-scope match.)
	refs := append(phpRefs(), model.SymbolRef{ID: 8, Qualified: "other.rb", FileID: 20, Language: "ruby"})
	ix := resolve.NewIndex(refs).WithInheritance(phpAncestry())
	r, ok := ix.Resolve(resolve.Request{
		Target:         `App\Sub\boot`,
		Kind:           model.EdgeCalls,
		SourceFileID:   20,
		BaseConfidence: extract.ConfidenceStatic,
	})
	if ok && r.SymbolID == 5 && r.Confidence == extract.ConfidenceStatic {
		t.Errorf("non-PHP source rode the PHP inherited lane at full confidence: %+v", r)
	}
}

func TestPHPNoAncestryFallsToLeaf(t *testing.T) {
	// Without ancestry the lane no-ops; the leaf fallback may still find
	// the method by bare name, demoted - never at full base confidence.
	ix := resolve.NewIndex(phpRefs())
	r, ok := ix.Resolve(resolve.Request{
		Target:         `App\Facades\Payments\charge`,
		Kind:           model.EdgeCalls,
		SourceFileID:   9,
		BaseConfidence: extract.ConfidenceStatic,
	})
	if ok && r.Confidence >= extract.ConfidenceStatic {
		t.Errorf("no-ancestry resolution at full confidence: %+v", r)
	}
}
