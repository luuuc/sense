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
			// Long enough that one strip clearly outweighs the truncated +
			// retained_trimmed flag bytes the strip itself adds; a
			// near-boundary name makes this a rounding test, not a shed-order
			// test.
			ID: 100 + int64(i), Name: "Carrier", Qualified: "pkg.SomeVeryLongCarrierTypeNameThatIsUnmistakablyWorthShedding", FileID: 1,
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
	anyChain := false
	for _, e := range squeezed.RetainedViaInterfaces {
		anyChain = anyChain || e.Chain != ""
	}
	for _, e := range squeezed.RetainedViaInterfaces {
		if e.Carrier == "" && anyChain {
			t.Errorf("a carrier stripped while chains remained (order inverted)")
		}
	}
}

// TestRetainedTrimmedFlagDisclosesEnrichmentShed: when the budget strips
// carriers or chains, retained_trimmed must be true: a shed field must be
// distinguishable from a never-computed one. The flag is set during the
// strip and priced through every later estimate, so the disclosure holds
// unconditionally in every band and can never surprise a later shed stage.
func TestRetainedTrimmedFlagDisclosesEnrichmentShed(t *testing.T) {
	ctx := context.Background()
	r := heavyRetainedResult(12)
	full := BuildBlastResponse(ctx, r, retainedFiles, nil)
	if full.RetainedTrimmed {
		t.Fatalf("unsqueezed response must not claim trimming")
	}
	fullTokens := estimateBlastWireTokens(&full)
	floor := strippedFloor(ctx, r)
	slackBudget := floor + 60
	if slackBudget >= fullTokens {
		t.Fatalf("fixture too light: floor=%d full=%d leaves no slack window", floor, fullTokens)
	}

	// Enrichment band: strips fire, rows survive, flag set, budget held.
	slack := BuildBlastResponse(ctx, r, retainedFiles, nil)
	ApplyBlastBudget(&slack, slackBudget)
	if len(slack.RetainedViaInterfaces) != len(full.RetainedViaInterfaces) {
		t.Fatalf("rows shed in the enrichment band: %d vs %d", len(slack.RetainedViaInterfaces), len(full.RetainedViaInterfaces))
	}
	if slack.RetainedViaInterfaces[len(slack.RetainedViaInterfaces)-1].Chain != "" {
		t.Fatalf("budget below full must strip enrichment")
	}
	if !slack.RetainedTrimmed {
		t.Errorf("enrichment stripped but retained_trimmed is false")
	}
	if estimateBlastWireTokens(&slack) > slackBudget {
		t.Errorf("response exceeds its budget in the enrichment band")
	}

	// Sub-floor: flag still true, budget held, and rows shed identically
	// to a control whose flag and strips are pre-applied, so the disclosure
	// never changes a shedding decision beyond its own priced bytes.
	for _, budget := range []int{floor - 1, floor - 40, floor - 120} {
		got := BuildBlastResponse(ctx, r, retainedFiles, nil)
		ApplyBlastBudget(&got, budget)
		control := BuildBlastResponse(ctx, r, retainedFiles, nil)
		for i := range control.RetainedViaInterfaces {
			control.RetainedViaInterfaces[i].Chain = ""
			control.RetainedViaInterfaces[i].Carrier = ""
		}
		control.Truncated = true
		control.RetainedTrimmed = true
		ApplyBlastBudget(&control, budget)
		if len(got.RetainedViaInterfaces) != len(control.RetainedViaInterfaces) {
			t.Errorf("budget %d: disclosure changed row shedding: %d rows vs control %d",
				budget, len(got.RetainedViaInterfaces), len(control.RetainedViaInterfaces))
		}
		if !got.RetainedTrimmed {
			t.Errorf("budget %d: enrichments stripped but retained_trimmed is false", budget)
		}
		if estimateBlastWireTokens(&got) > budget {
			t.Errorf("budget %d: response exceeds its budget", budget)
		}
	}
}

// heavyRetainedResult builds a hub-shaped result: n retained rows with
// long chains, so stripping enrichments frees far more than the
// disclosure sentence costs (the 2-row fixture's window was empty and
// hid a vacuous assertion).
func heavyRetainedResult(n int) blast.Result {
	r := retainedResult(true)
	rows := make([]blast.RetainedHolder, 0, n)
	for i := range n {
		id := int64(200 + i*10)
		rows = append(rows, blast.RetainedHolder{
			Symbol:  model.Symbol{ID: id, Name: "Holder", Qualified: "pkg.subsystem.HolderNumber" + string(rune('A'+i)), FileID: 2, LineStart: 30 + i, LineEnd: 40 + i},
			Via:     model.Symbol{ID: id + 1, Name: "RareIface", Qualified: "pkg.RareIface", FileID: 1},
			Carrier: model.Symbol{ID: id + 2, Name: "Carrier", Qualified: "pkg.deep.CarrierImplementation", FileID: 1},
			Chain: []model.Symbol{
				{ID: id + 2, Name: "Carrier", Qualified: "pkg.deep.CarrierImplementation", FileID: 1},
				{ID: id + 3, Name: "Mid", Qualified: "pkg.middle.IntermediateContainer", FileID: 1},
				{ID: id + 4, Name: "Inner", Qualified: "pkg.inner.InnerHolderStructure", FileID: 1},
				{ID: 1, Name: "Widget", Qualified: "Widget", FileID: 1},
			},
		})
	}
	r.RetainedViaInterfaces = rows
	r.RetainedCount = n
	return r
}

// strippedFloor is the wire size of the fixture with every enrichment
// removed: the point below which whole rows must shed.
func strippedFloor(ctx context.Context, r blast.Result) int {
	resp := BuildBlastResponse(ctx, r, retainedFiles, nil)
	for i := range resp.RetainedViaInterfaces {
		resp.RetainedViaInterfaces[i].Chain = ""
		resp.RetainedViaInterfaces[i].Carrier = ""
	}
	resp.Truncated = true
	return estimateBlastWireTokens(&resp)
}

// --- Purity disclosure on the wire ---

// TestRetainedNoteDisclosesExclusions: the refused test-file satisfiers ride
// out in the note with their count and a bounded name sample, so a purified
// ring reads as purified rather than as a short one.
func TestRetainedNoteDisclosesExclusions(t *testing.T) {
	r := retainedResult(true)
	r.RetainedPurity = blast.RetentionPurity{Excluded: 7, Names: []string{"FakeA", "FakeB", "FakeC", "FakeD", "FakeE"}}

	resp := BuildBlastResponse(context.Background(), r, func(int64) (string, bool) { return "widget.go", true }, nil)

	for _, want := range []string{"7 test-file satisfier(s) refused as carriers", "FakeA", "FakeE", "…", "contribute no rows"} {
		if !strings.Contains(resp.RetainedNote, want) {
			t.Errorf("retained_note missing %q: %s", want, resp.RetainedNote)
		}
	}
	if !strings.Contains(resp.RetainedNote, "may retain Widget") {
		t.Errorf("retained_note dropped the may-claim sentence: %s", resp.RetainedNote)
	}
}

// TestRetainedNoteFullyExcludedRing: when purity empties the ring, the note is
// still emitted: an empty ring and a ring emptied by the filter are different
// facts, and only the note tells them apart.
func TestRetainedNoteFullyExcludedRing(t *testing.T) {
	r := retainedResult(false)
	r.RetainedPurity = blast.RetentionPurity{Excluded: 2, Names: []string{"FakeA", "FakeB"}}

	resp := BuildBlastResponse(context.Background(), r, func(int64) (string, bool) { return "widget.go", true }, nil)

	if len(resp.RetainedViaInterfaces) != 0 {
		t.Fatalf("expected an empty ring, got %+v", resp.RetainedViaInterfaces)
	}
	if !strings.Contains(resp.RetainedNote, "2 test-file satisfier(s) refused as carriers: FakeA, FakeB") {
		t.Errorf("retained_note = %q, want the exclusion disclosure", resp.RetainedNote)
	}
	if strings.Contains(resp.RetainedNote, "may retain") {
		t.Errorf("no rows, so no may-claim sentence belongs in the note: %q", resp.RetainedNote)
	}
}

// --- Promiscuity stamp on the wire ---

// TestRetainedRowCarriesViaSatisfiers: the stamp reaches the wire as a bare
// count, the note explains what it measures, and a row with no count omits
// the key rather than claiming zero satisfiers.
func TestRetainedRowCarriesViaSatisfiers(t *testing.T) {
	r := retainedResult(true)
	r.RetainedViaInterfaces[0].ViaSatisfiers = 23

	resp := BuildBlastResponse(context.Background(), r, func(int64) (string, bool) { return "widget.go", true }, nil)

	if resp.RetainedViaInterfaces[0].ViaSatisfiers != 23 {
		t.Errorf("via_satisfiers = %d, want 23", resp.RetainedViaInterfaces[0].ViaSatisfiers)
	}
	if !strings.Contains(resp.RetainedNote, "`via_satisfiers` counts concretes satisfying `via`") {
		t.Errorf("retained_note does not explain the stamp: %s", resp.RetainedNote)
	}
	raw, err := json.Marshal(resp.RetainedViaInterfaces[1])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "via_satisfiers") {
		t.Errorf("an unstamped row must omit via_satisfiers, got %s", raw)
	}
}

// --- Paging on the wire ---

// pagedResult is a ring page: 2 rows shown starting at offset 3 of 9.
func pagedResult() blast.Result {
	r := retainedResult(true)
	r.RetainedCount = 9
	r.RetainedOffset = 3
	r.RetainedFingerprint = "f12@2026-07-20T10:00:00Z"
	return r
}

// TestRetainedPageLocatesItself: a page states which rows it is, how many
// exist, the offset that resumes it, and the generation it was cut from ,
// entirely in structured fields. A short ring must never be indistinguishable
// from a complete one, and it must not spend a holder's worth of bytes saying
// so (the fields cost a fraction of the sentence they replace).
func TestRetainedPageLocatesItself(t *testing.T) {
	resp := BuildBlastResponse(context.Background(), pagedResult(), retainedFiles, nil)

	if resp.RetainedOffset != 3 {
		t.Errorf("retained_offset = %d, want 3", resp.RetainedOffset)
	}
	if resp.RetainedNextOffset != 5 {
		t.Errorf("retained_next_offset = %d, want 5 (3 skipped + 2 shown)", resp.RetainedNextOffset)
	}
	if resp.RetainedCount != 9 {
		t.Errorf("retained_via_interfaces_count = %d, want the full ring size 9", resp.RetainedCount)
	}
	if resp.RetainedFingerprint != "f12@2026-07-20T10:00:00Z" {
		t.Errorf("retained_index_fingerprint = %q, want the generation stamp", resp.RetainedFingerprint)
	}
	// The page facts live in fields ONLY: prose restating them would cost a
	// holder on a ring that saturates the budget.
	for _, banned := range []string{"rows 4-5", "call again", "offset:5"} {
		if strings.Contains(resp.RetainedNote, banned) {
			t.Errorf("paging prose %q must not ride the note: %s", banned, resp.RetainedNote)
		}
	}
}

// TestRetainedLastPageOffersNoNextOffset: a page that ends the ring must not
// advertise a resume point that would return nothing.
func TestRetainedLastPageOffersNoNextOffset(t *testing.T) {
	r := pagedResult()
	r.RetainedCount = 5 // 3 skipped + 2 shown = the end

	resp := BuildBlastResponse(context.Background(), r, retainedFiles, nil)

	if resp.RetainedNextOffset != 0 {
		t.Errorf("retained_next_offset = %d, want 0 at the end of the ring", resp.RetainedNextOffset)
	}
	// It is still a page (offset 3), so the fingerprint stays: the consumer
	// needs it to know this page unions with the one before it.
	if resp.RetainedFingerprint == "" {
		t.Error("a page must carry its index fingerprint even when it ends the ring")
	}
}

// TestBudgetShedReRendersPaging: when the budget sheds ring rows, the page
// boundary moves with them: the note and retained_next_offset describe the
// rows that actually shipped, so a squeezed ring is resumable instead of
// silently short.
func TestBudgetShedReRendersPaging(t *testing.T) {
	resp := BuildBlastResponse(context.Background(), pagedResult(), retainedFiles, nil)
	resp.IndirectCallers = nil

	ApplyBlastBudget(&resp, 1) // force every trim step

	if len(resp.RetainedViaInterfaces) != 0 {
		t.Fatalf("expected the ring to shed entirely, got %d rows", len(resp.RetainedViaInterfaces))
	}
	if resp.RetainedNextOffset != 3 {
		t.Errorf("retained_next_offset = %d, want 3: the shed page resumes where it started", resp.RetainedNextOffset)
	}
	if resp.RetainedFingerprint == "" {
		t.Error("a shed page still needs its fingerprint to be resumable")
	}
	if resp.RetainedCount != 9 {
		t.Errorf("retained_via_interfaces_count = %d, want 9 (never reduced)", resp.RetainedCount)
	}
}

// TestBudgetShedOfOneRowMovesTheBoundary pins the partial case: one row shed
// moves the resume offset back by exactly one row, never to zero.
func TestBudgetShedOfOneRowMovesTheBoundary(t *testing.T) {
	resp := BuildBlastResponse(context.Background(), pagedResult(), retainedFiles, nil)
	resp.RetainedViaInterfaces = resp.RetainedViaInterfaces[:1]
	resolveRetainedPage(&resp)

	if resp.RetainedNextOffset != 4 {
		t.Errorf("retained_next_offset = %d, want 4", resp.RetainedNextOffset)
	}
	if resp.RetainedCount != 9 {
		t.Errorf("retained_via_interfaces_count = %d, want 9 (never reduced)", resp.RetainedCount)
	}
}

// --- Facts and the may-claim stay separated ---

// TestRetainedNoteSeparatesFactsFromClaim: the note leads with what the index
// holds and names exactly one unverified thing. An agent that reads only the
// first clause must come away with facts, never with a guess dressed as one.
func TestRetainedNoteSeparatesFactsFromClaim(t *testing.T) {
	r := retainedResult(true)
	r.RetainedPurity = blast.RetentionPurity{Excluded: 1, Names: []string{"FakeA"}}

	resp := BuildBlastResponse(context.Background(), r, retainedFiles, nil)
	note := resp.RetainedNote

	facts := strings.Index(note, "as indexed")
	claim := strings.Index(note, "Unverified, and the only unverified part")
	excl := strings.Index(note, "refused as carriers")
	if facts != 0 {
		t.Errorf("note must LEAD with the structural facts, got: %s", note)
	}
	if claim < facts || excl < claim {
		t.Errorf("note order must be facts → may-claim → exclusions, got: %s", note)
	}
	// The two load-bearing phrases of the shipped group contract.
	for _, want := range []string{"may retain Widget", "one interface indirection"} {
		if !strings.Contains(note, want) {
			t.Errorf("note dropped the pinned phrase %q: %s", want, note)
		}
	}
}

// TestStampShedsBeforeAnyHolder is the shed-hierarchy threshold pin: at a
// budget ONE token under what the stamped response needs, the
// via_satisfiers annotation must go and every holder must stay. A row is the
// answer; the stamp is a caution about the answer. Without this pin the
// ordering silently regressed and cost dolt 2 of its 12 gold rows.
func TestStampShedsBeforeAnyHolder(t *testing.T) {
	r := retainedResult(true)
	for i := range r.RetainedViaInterfaces {
		r.RetainedViaInterfaces[i].ViaSatisfiers = 23
	}
	resp := BuildBlastResponse(context.Background(), r, retainedFiles, nil)
	resp.IndirectCallers = nil
	rows := len(resp.RetainedViaInterfaces)

	ApplyBlastBudget(&resp, estimateBlastWireTokens(&resp)-1)

	if len(resp.RetainedViaInterfaces) != rows {
		t.Fatalf("holders = %d, want all %d: a stamp must shed before any row", len(resp.RetainedViaInterfaces), rows)
	}
	if anyRetainedStamped(resp.RetainedViaInterfaces) {
		t.Error("the tail stamp must shed at one token over budget")
	}
	if strings.Contains(resp.RetainedNote, "via_satisfiers") {
		t.Errorf("the sentence explaining a shed stamp must retire with it: %s", resp.RetainedNote)
	}
}
