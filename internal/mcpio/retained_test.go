package mcpio

// Wire-shape pins for the retained_via_interfaces group (pitch 31-12): the
// group is omitted entirely when empty — byte-identity for languages without
// interface symbols depends on ALL THREE keys (list, count, note) vanishing
// from the marshaled response, not serializing as empty values.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/luuuc/sense/internal/blast"
	"github.com/luuuc/sense/internal/model"
)

// TestBlastResponseOmitsEmptyRetainedGroup pins the empty case: a Result with
// no retained holders marshals with no retained_* keys at all.
func TestBlastResponseOmitsEmptyRetainedGroup(t *testing.T) {
	r := blast.Result{
		Symbol:        model.Symbol{ID: 1, Name: "Widget", Qualified: "Widget"},
		Risk:          blast.RiskLow,
		RiskReasons:   []string{"0 direct callers"},
		AffectedTests: []string{},
	}
	resp := BuildBlastResponse(context.Background(), r, func(int64) (string, bool) { return "", false }, nil)

	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{"retained_via_interfaces", "retained_via_interfaces_count", "retained_note"} {
		if strings.Contains(string(raw), key) {
			t.Errorf("empty retained group must omit %q, got: %s", key, raw)
		}
	}
}

// retainedResult builds a Result with one direct caller and two retained
// holders — the comparison base for the pins below.
func retainedResult(withRetained bool) blast.Result {
	r := blast.Result{
		Symbol:        model.Symbol{ID: 1, Name: "Widget", Qualified: "Widget", FileID: 1},
		Risk:          blast.RiskLow,
		RiskReasons:   []string{"1 direct caller"},
		AffectedTests: []string{},
		DirectCallers: []model.Symbol{
			{ID: 2, Name: "CarrierC", Qualified: "CarrierC", FileID: 1, LineStart: 10},
		},
		TotalAffected: 1,
	}
	if withRetained {
		r.RetainedViaInterfaces = []blast.RetainedHolder{
			{Symbol: model.Symbol{ID: 5, Name: "HolderH", Qualified: "HolderH", FileID: 2, LineStart: 30, LineEnd: 40},
				Via: model.Symbol{ID: 3, Name: "RareIface", Qualified: "RareIface", FileID: 1}},
			{Symbol: model.Symbol{ID: 6, Name: "HolderK", Qualified: "HolderK", FileID: 2, LineStart: 50, LineEnd: 60},
				Via: model.Symbol{ID: 4, Name: "OtherIface", Qualified: "OtherIface", FileID: 1}},
		}
		r.RetainedCount = 2
	}
	return r
}

func retainedFiles(id int64) (string, bool) {
	switch id {
	case 1:
		return "app/widget.go", true
	case 2:
		return "app/holder.go", true
	}
	return "", false
}

// TestBuildBlastResponseRendersRetainedGroup: the group renders with the
// may-retain relation naming the via-interface, the full count, the depth-1
// note — and it stays OUT of every existing accounting surface
// (references.count, production/test segmentation, affected_files,
// total_affected, completeness).
func TestBuildBlastResponseRendersRetainedGroup(t *testing.T) {
	ctx := context.Background()
	base := BuildBlastResponse(ctx, retainedResult(false), retainedFiles, nil)
	resp := BuildBlastResponse(ctx, retainedResult(true), retainedFiles, nil)

	if len(resp.RetainedViaInterfaces) != 2 {
		t.Fatalf("retained entries = %d, want 2", len(resp.RetainedViaInterfaces))
	}
	first := resp.RetainedViaInterfaces[0]
	if first.Via != "RareIface" {
		t.Errorf("via = %q, want the via-interface name", first.Via)
	}
	if first.Ref != "app/holder.go:30" {
		t.Errorf("entry must carry the file:line ref, got %q", first.Ref)
	}
	if first.Symbol != "HolderH" {
		t.Errorf("symbol = %q, want HolderH", first.Symbol)
	}
	if resp.RetainedCount != 2 {
		t.Errorf("RetainedCount = %d, want 2", resp.RetainedCount)
	}
	if !strings.Contains(resp.RetainedNote, "one interface indirection") {
		t.Errorf("group note must state the depth-1 bound, got %q", resp.RetainedNote)
	}
	if !strings.Contains(resp.RetainedNote, "may retain Widget") {
		t.Errorf("group note must carry the may-retain semantics once, got %q", resp.RetainedNote)
	}

	// Exclusion pins: every existing accounting surface is byte-equal.
	if resp.References.Count != base.References.Count {
		t.Errorf("references.count changed: %d vs %d", resp.References.Count, base.References.Count)
	}
	if resp.ProductionAffected != base.ProductionAffected || resp.TestAffected != base.TestAffected {
		t.Errorf("segmentation changed: prod %d vs %d, test %d vs %d",
			resp.ProductionAffected, base.ProductionAffected, resp.TestAffected, base.TestAffected)
	}
	if resp.AffectedFiles != base.AffectedFiles {
		t.Errorf("affected_files changed: %d vs %d", resp.AffectedFiles, base.AffectedFiles)
	}
	if resp.TotalAffected != base.TotalAffected {
		t.Errorf("total_affected changed: %d vs %d", resp.TotalAffected, base.TotalAffected)
	}
	if resp.Completeness == nil || base.Completeness == nil ||
		resp.Completeness.Verdict != base.Completeness.Verdict {
		t.Errorf("completeness verdict must not react to the retained group")
	}
	if resp.SenseMetrics != base.SenseMetrics {
		t.Errorf("sense metrics changed: %+v vs %+v", resp.SenseMetrics, base.SenseMetrics)
	}
}

// TestApplyBlastBudgetTrimsRetainedBeforeDirect: under budget pressure the
// retained entries shed before any direct caller, the count survives
// untrimmed, and the response flags truncation.
func TestApplyBlastBudgetTrimsRetainedBeforeDirect(t *testing.T) {
	resp := BuildBlastResponse(context.Background(), retainedResult(true), retainedFiles, nil)
	resp.IndirectCallers = nil // isolate the retained trim step

	tiny := 1 // force every trim step to fire
	ApplyBlastBudget(&resp, tiny)

	if len(resp.RetainedViaInterfaces) != 0 {
		t.Errorf("retained entries must shed under budget, got %d", len(resp.RetainedViaInterfaces))
	}
	if resp.RetainedCount != 2 {
		t.Errorf("RetainedCount = %d, want 2 (never reduced by trimming)", resp.RetainedCount)
	}
	if len(resp.DirectCallers) != 1 {
		t.Errorf("the last direct caller must survive, got %d", len(resp.DirectCallers))
	}
	if !resp.Truncated {
		t.Errorf("Truncated must be set")
	}
}

// TestApplyBlastBudgetShedsDuplicativeContentFirst: under pressure the tier-2
// reference examples (duplicates of fully-enumerated group lists) and the
// affected-test sample empty BEFORE any retained entry sheds, and their
// counts survive.
func TestApplyBlastBudgetShedsDuplicativeContentFirst(t *testing.T) {
	resp := BuildBlastResponse(context.Background(), retainedResult(true), retainedFiles, nil)
	resp.AffectedTests = []string{"a_test.go", "b_test.go"}
	resp.TestsAffectedCount = 2
	resp.References = BlastTierSummary{Count: 7, Examples: []BlastCaller{{Symbol: "Dup", File: "app/widget.go"}}}

	// A budget wide enough that shedding examples+tests suffices: current
	// size minus just those two lists.
	over := estimateBlastWireTokens(&resp) - 1
	ApplyBlastBudget(&resp, over)

	if len(resp.References.Examples) != 0 {
		t.Errorf("reference examples must shed first, got %d", len(resp.References.Examples))
	}
	if resp.References.Count != 7 {
		t.Errorf("references.count must survive, got %d", resp.References.Count)
	}
	if resp.TestsAffectedCount != 2 {
		t.Errorf("tests_affected_count must survive, got %d", resp.TestsAffectedCount)
	}
	if len(resp.RetainedViaInterfaces) != 2 {
		t.Errorf("retained entries must not shed while duplicative content remains, got %d", len(resp.RetainedViaInterfaces))
	}
	if !resp.Truncated {
		t.Errorf("Truncated must be set")
	}
}

// TestApplyBlastBudgetKeepsDirectCallersWhileSheddingRetained pins the shed
// ORDER between steps 3d and 4: with a budget that shedding retained alone
// satisfies, every direct caller survives — a weaker may-claim must never
// outlive a stronger one.
func TestApplyBlastBudgetKeepsDirectCallersWhileSheddingRetained(t *testing.T) {
	r := retainedResult(true)
	for i := int64(0); i < 4; i++ {
		r.DirectCallers = append(r.DirectCallers, model.Symbol{
			ID: 10 + i, Name: fmt.Sprintf("Caller%d", i), Qualified: fmt.Sprintf("Caller%d", i),
			FileID: 1, LineStart: int(100 + i)})
	}
	r.TotalAffected = len(r.DirectCallers)
	resp := BuildBlastResponse(context.Background(), r, retainedFiles, nil)
	resp.AffectedTests = nil
	resp.References.Examples = nil

	noRetained := resp
	noRetained.RetainedViaInterfaces = nil
	// +8 covers the `"truncated":true` the shed itself adds; still far under
	// one retained entry's cost, so only the retained shed can satisfy it.
	budget := estimateBlastWireTokens(&noRetained) + 8

	ApplyBlastBudget(&resp, budget)

	if len(resp.DirectCallers) != 5 {
		t.Errorf("direct callers = %d, want all 5 (retained must shed before any direct caller)", len(resp.DirectCallers))
	}
	if len(resp.RetainedViaInterfaces) >= 2 {
		t.Errorf("retained entries = %d, want trimmed below 2", len(resp.RetainedViaInterfaces))
	}
	if resp.RetainedCount != 2 {
		t.Errorf("RetainedCount = %d, want 2", resp.RetainedCount)
	}
}

// TestRetainedEntryCarriesConcreteCarrier: the wire entry names the concrete
// carrier the laundering round proved, so the consumer writes the retention
// row without a per-interface graph join. Name only: the group must stay
// lean enough to survive the hub-subject budget.
func TestRetainedEntryCarriesConcreteCarrier(t *testing.T) {
	ctx := context.Background()
	r := retainedResult(true)
	r.RetainedViaInterfaces[0].Carrier = model.Symbol{ID: 2, Name: "CarrierC", Qualified: "pkg.CarrierC", FileID: 1}
	resp := BuildBlastResponse(ctx, r, retainedFiles, nil)

	if got := resp.RetainedViaInterfaces[0].Carrier; got != "pkg.CarrierC" {
		t.Errorf("carrier = %q, want %q", got, "pkg.CarrierC")
	}
	// A zero-valued carrier (defensive: hydration miss) renders empty and is
	// omitted from the wire, never rendered as a phantom name.
	if got := resp.RetainedViaInterfaces[1].Carrier; got != "" {
		t.Errorf("carrier for zero-value = %q, want empty", got)
	}
}

// TestRetainedCarrierShedsBeforeRows: under budget pressure the carrier
// names strip tail-first (count- and row-preserving) BEFORE any whole
// retained row sheds; a row is strictly worth more than its enrichment.
// Kills both mutants: skipping the carrier shed (rows drop while carriers
// survive) and inverting its order (head-first stripping).
func TestRetainedCarrierShedsBeforeRows(t *testing.T) {
	ctx := context.Background()
	r := retainedResult(true)
	for i := range r.RetainedViaInterfaces {
		r.RetainedViaInterfaces[i].Carrier = model.Symbol{
			ID: 100 + int64(i), Name: "Carrier", Qualified: "pkg.SomeVeryLongCarrierTypeName", FileID: 1,
		}
	}
	full := BuildBlastResponse(ctx, r, retainedFiles, nil)
	fullTokens := estimateBlastWireTokens(&full)

	// A budget just below the full size must strip a tail carrier, not a row.
	squeezed := BuildBlastResponse(ctx, r, retainedFiles, nil)
	ApplyBlastBudget(&squeezed, fullTokens-1)
	if len(squeezed.RetainedViaInterfaces) != len(full.RetainedViaInterfaces) {
		t.Fatalf("rows shed before carriers: %d rows, want %d", len(squeezed.RetainedViaInterfaces), len(full.RetainedViaInterfaces))
	}
	last := len(squeezed.RetainedViaInterfaces) - 1
	if squeezed.RetainedViaInterfaces[last].Carrier != "" {
		t.Errorf("tail carrier survived a squeeze that required shedding")
	}
	if squeezed.RetainedViaInterfaces[0].Carrier == "" {
		t.Errorf("head carrier stripped before tail (order inverted)")
	}
}

// TestRetainedEntryRendersChainAndShedOrder: the chain renders as a
// ">"-joined containment path (a statable structural fact), and under
// pressure enrichments strip in evidence-weight order: chains tail-first,
// then carriers, then whole rows.
func TestRetainedEntryRendersChainAndShedOrder(t *testing.T) {
	ctx := context.Background()
	r := retainedResult(true)
	for i := range r.RetainedViaInterfaces {
		r.RetainedViaInterfaces[i].Carrier = model.Symbol{ID: 100 + int64(i), Name: "Carrier", Qualified: "pkg.CarrierType", FileID: 1}
		r.RetainedViaInterfaces[i].Chain = []model.Symbol{
			{ID: 100 + int64(i), Name: "Carrier", Qualified: "pkg.CarrierType", FileID: 1},
			{ID: 1, Name: "Widget", Qualified: "Widget", FileID: 1},
		}
	}
	full := BuildBlastResponse(ctx, r, retainedFiles, nil)
	if got := full.RetainedViaInterfaces[0].Chain; got != "pkg.CarrierType > Widget" {
		t.Fatalf("chain = %q, want %q", got, "pkg.CarrierType > Widget")
	}
	fullTokens := estimateBlastWireTokens(&full)

	squeezed := BuildBlastResponse(ctx, r, retainedFiles, nil)
	ApplyBlastBudget(&squeezed, fullTokens-1)
	if len(squeezed.RetainedViaInterfaces) != len(full.RetainedViaInterfaces) {
		t.Fatalf("rows shed before enrichments")
	}
	last := len(squeezed.RetainedViaInterfaces) - 1
	if squeezed.RetainedViaInterfaces[last].Chain != "" {
		t.Errorf("tail chain survived a squeeze that required shedding")
	}
	if squeezed.RetainedViaInterfaces[last].Carrier == "" {
		t.Errorf("carrier stripped while chains remained (order inverted)")
	}
}
