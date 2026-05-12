package mcpio

import (
	"fmt"
	"testing"

	"github.com/luuuc/sense/internal/blast"
	"github.com/luuuc/sense/internal/model"
)

// noFiles is a FileLookup that never resolves — the diff-union
// tests below exercise aggregation logic, not path hydration. Every
// BlastCaller.File will be "" under this lookup, which matches the
// documented "lookup miss → empty string" contract.
var noFiles FileLookup = func(int64) (string, bool) { return "", false }

// TestBuildDiffBlastResponseMaxRisk pins the conservative risk
// aggregation: if any modified subject classifies as "high", the
// unioned Risk is "high"; if the max is "medium", the union is
// "medium"; all-low → low.
func TestBuildDiffBlastResponseMaxRisk(t *testing.T) {
	tests := []struct {
		name  string
		risks []string
		want  string
	}{
		{"all low", []string{blast.RiskLow, blast.RiskLow}, blast.RiskLow},
		{"low then medium", []string{blast.RiskLow, blast.RiskMedium}, blast.RiskMedium},
		{"medium then high", []string{blast.RiskMedium, blast.RiskHigh}, blast.RiskHigh},
		{"high then low", []string{blast.RiskHigh, blast.RiskLow}, blast.RiskHigh},
		{"empty", nil, blast.RiskLow},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			results := make([]blast.Result, len(tc.risks))
			for i, r := range tc.risks {
				results[i] = blast.Result{Risk: r}
			}
			resp := BuildDiffBlastResponse("HEAD~1", results, noFiles)
			if resp.Risk != tc.want {
				t.Errorf("Risk = %q, want %q", resp.Risk, tc.want)
			}
		})
	}
}

// TestBuildDiffBlastResponseDedupCallers pins that a direct caller
// shared by two modified subjects appears exactly once in the
// unioned response. This is load-bearing for honest `total_affected`
// counts — without dedup, a subject appearing N times in the diff
// multiplies its callers N times.
func TestBuildDiffBlastResponseDedupCallers(t *testing.T) {
	// Shared caller C (id=42) depends on both modified subjects.
	sharedCaller := model.Symbol{ID: 42, Qualified: "C#calls_both", FileID: 7}
	indirectHop := blast.CallerHop{
		Symbol: model.Symbol{ID: 100, Qualified: "I#indirect", FileID: 8},
		Via:    sharedCaller,
		Hops:   2,
	}

	results := []blast.Result{
		{
			Symbol:          model.Symbol{ID: 1, Qualified: "A"},
			Risk:            blast.RiskLow,
			DirectCallers:   []model.Symbol{sharedCaller},
			IndirectCallers: []blast.CallerHop{indirectHop},
			AffectedTests:   []string{"test/a_test.rb", "test/shared_test.rb"},
		},
		{
			Symbol:          model.Symbol{ID: 2, Qualified: "B"},
			Risk:            blast.RiskLow,
			DirectCallers:   []model.Symbol{sharedCaller},
			IndirectCallers: []blast.CallerHop{indirectHop},
			AffectedTests:   []string{"test/b_test.rb", "test/shared_test.rb"},
		},
	}

	resp := BuildDiffBlastResponse("HEAD~1", results, noFiles)
	if len(resp.DirectCallers) != 1 {
		t.Errorf("DirectCallers = %d, want 1 (shared caller appears in both subjects)",
			len(resp.DirectCallers))
	}
	if len(resp.IndirectCallers) != 1 {
		t.Errorf("IndirectCallers = %d, want 1", len(resp.IndirectCallers))
	}
	// Tests dedup by path: a_test, b_test, shared_test = 3 unique.
	if len(resp.AffectedTests) != 3 {
		t.Errorf("AffectedTests = %d, want 3 (a + b + shared, shared appears once)",
			len(resp.AffectedTests))
	}
	// total_affected counts callers after dedup; tests are separate.
	if resp.TotalAffected != 2 {
		t.Errorf("TotalAffected = %d, want 2 (1 direct + 1 indirect)", resp.TotalAffected)
	}
}

func TestBuildBlastResponseViaTemporal(t *testing.T) {
	r := blast.Result{
		Symbol: model.Symbol{ID: 1, Qualified: "Subject"},
		Risk:   blast.RiskMedium,
		RiskReasons: []string{
			"0 direct callers",
			"temporal coupling detected (git co-change history)",
		},
		DirectCallers: []model.Symbol{
			{ID: 10, Qualified: "TemporalPartner", FileID: 2},
		},
		IndirectCallers: []blast.CallerHop{
			{
				Symbol:      model.Symbol{ID: 20, Qualified: "Indirect"},
				Via:         model.Symbol{ID: 10, Qualified: "TemporalPartner"},
				Hops:        2,
				ViaTemporal: true,
			},
		},
		AffectedTests:     []string{},
		TotalAffected:     2,
		DirectTemporalIDs: map[int64]bool{10: true},
	}

	filePaths := map[int64]string{2: "partner.rb"}
	files := func(id int64) (string, bool) {
		p, ok := filePaths[id]
		return p, ok
	}

	resp := BuildBlastResponse(r, files)

	if len(resp.DirectCallers) != 1 {
		t.Fatalf("DirectCallers = %d, want 1", len(resp.DirectCallers))
	}
	if !resp.DirectCallers[0].ViaTemporal {
		t.Error("DirectCallers[0].ViaTemporal should be true")
	}

	if len(resp.IndirectCallers) != 1 {
		t.Fatalf("IndirectCallers = %d, want 1", len(resp.IndirectCallers))
	}
	if !resp.IndirectCallers[0].ViaTemporal {
		t.Error("IndirectCallers[0].ViaTemporal should be true")
	}

	if resp.Risk != blast.RiskMedium {
		t.Errorf("Risk = %q, want medium", resp.Risk)
	}
}

func TestBuildBlastResponseTier1Cap(t *testing.T) {
	// Build a Result with 250 Tier-1 (calls-edge) direct callers.
	// The response should cap at 200.
	var directCallers []model.Symbol
	tiers := make(map[int64]blast.Tier)
	for i := int64(1); i <= 250; i++ {
		directCallers = append(directCallers, model.Symbol{
			ID: i, Qualified: fmt.Sprintf("Caller%d", i),
			FileID: 100,
		})
		tiers[i] = blast.TierBreaks
	}

	r := blast.Result{
		Symbol:        model.Symbol{ID: 0, Qualified: "Subject"},
		Risk:          blast.RiskHigh,
		RiskReasons:   []string{"250 direct callers"},
		DirectCallers: directCallers,
		AffectedTests: []string{},
		TotalAffected: 250,
		SymbolTiers:   tiers,
	}

	resp := BuildBlastResponse(r, noFiles)

	if len(resp.DirectCallers) != 200 {
		t.Errorf("DirectCallers = %d, want 200 (tier1 cap)", len(resp.DirectCallers))
	}
	if resp.TotalAffected != 250 {
		t.Errorf("TotalAffected = %d, want 250 (pre-cap count preserved)", resp.TotalAffected)
	}
}

func TestBuildBlastResponseTierPartitioning(t *testing.T) {
	// 3 Tier-1 callers and 2 Tier-2 callers. Tier-2 items should
	// appear in References, not DirectCallers.
	tiers := map[int64]blast.Tier{
		1: blast.TierBreaks,
		2: blast.TierBreaks,
		3: blast.TierBreaks,
		4: blast.TierReferences,
		5: blast.TierReferences,
	}

	r := blast.Result{
		Symbol:      model.Symbol{ID: 0, Qualified: "Subject"},
		Risk:        blast.RiskLow,
		RiskReasons: []string{"5 direct callers"},
		DirectCallers: []model.Symbol{
			{ID: 1, Qualified: "A", FileID: 10},
			{ID: 2, Qualified: "B", FileID: 10},
			{ID: 3, Qualified: "C", FileID: 10},
			{ID: 4, Qualified: "D", FileID: 10},
			{ID: 5, Qualified: "E", FileID: 10},
		},
		AffectedTests: []string{},
		TotalAffected: 5,
		SymbolTiers:   tiers,
	}

	resp := BuildBlastResponse(r, noFiles)

	if len(resp.DirectCallers) != 3 {
		t.Errorf("DirectCallers = %d, want 3 (Tier 1 only)", len(resp.DirectCallers))
	}
	if resp.References.Count != 2 {
		t.Errorf("References.Count = %d, want 2", resp.References.Count)
	}
	if len(resp.References.Examples) != 2 {
		t.Errorf("References.Examples = %d, want 2 (both shown, under cap)", len(resp.References.Examples))
	}
}

func TestBuildBlastResponseEdgeKindGroups(t *testing.T) {
	r := blast.Result{
		Symbol:      model.Symbol{ID: 0, Qualified: "Base"},
		Risk:        blast.RiskLow,
		RiskReasons: []string{"5 direct callers"},
		DirectCallers: []model.Symbol{
			{ID: 1, Qualified: "Caller", FileID: 10},
		},
		AffectedSubclasses: []model.Symbol{
			{ID: 2, Qualified: "SubA", FileID: 11},
		},
		AffectedViaComposition: []model.Symbol{
			{ID: 3, Qualified: "CompX", FileID: 12},
		},
		AffectedViaIncludes: []model.Symbol{
			{ID: 4, Qualified: "InclY", FileID: 13},
		},
		AffectedTests: []string{},
		TotalAffected: 4,
		SymbolTiers: map[int64]blast.Tier{
			1: blast.TierBreaks,
			2: blast.TierReferences,
			3: blast.TierReferences,
			4: blast.TierReferences,
		},
	}

	files := func(id int64) (string, bool) {
		m := map[int64]string{10: "caller.rb", 11: "sub.rb", 12: "comp.rb", 13: "incl.rb"}
		p, ok := m[id]
		return p, ok
	}

	resp := BuildBlastResponse(r, files)

	if len(resp.AffectedSubclasses) != 1 || resp.AffectedSubclasses[0].Symbol != "SubA" {
		t.Errorf("AffectedSubclasses = %+v, want [SubA]", resp.AffectedSubclasses)
	}
	if len(resp.AffectedViaComposition) != 1 || resp.AffectedViaComposition[0].Symbol != "CompX" {
		t.Errorf("AffectedViaComposition = %+v, want [CompX]", resp.AffectedViaComposition)
	}
	if len(resp.AffectedViaIncludes) != 1 || resp.AffectedViaIncludes[0].Symbol != "InclY" {
		t.Errorf("AffectedViaIncludes = %+v, want [InclY]", resp.AffectedViaIncludes)
	}
	if resp.References.Count != 3 {
		t.Errorf("References.Count = %d, want 3 (subclass + comp + incl)", resp.References.Count)
	}
	if resp.ProductionAffected != 4 {
		t.Errorf("ProductionAffected = %d, want 4", resp.ProductionAffected)
	}
}

func TestBuildBlastResponseIndirectTier2(t *testing.T) {
	r := blast.Result{
		Symbol:        model.Symbol{ID: 0, Qualified: "Subject"},
		Risk:          blast.RiskLow,
		RiskReasons:   []string{"1 direct caller"},
		DirectCallers: []model.Symbol{},
		IndirectCallers: []blast.CallerHop{
			{
				Symbol: model.Symbol{ID: 10, Qualified: "Tier1Indirect", FileID: 1},
				Via:    model.Symbol{ID: 0, Qualified: "Subject"},
				Hops:   2,
			},
			{
				Symbol: model.Symbol{ID: 11, Qualified: "Tier2Indirect", FileID: 2},
				Via:    model.Symbol{ID: 10, Qualified: "Tier1Indirect"},
				Hops:   3,
			},
		},
		AffectedTests: []string{},
		TotalAffected: 2,
		SymbolTiers: map[int64]blast.Tier{
			10: blast.TierBreaks,
			11: blast.TierReferences,
		},
	}

	files := func(id int64) (string, bool) {
		m := map[int64]string{1: "a.rb", 2: "b.rb"}
		p, ok := m[id]
		return p, ok
	}

	resp := BuildBlastResponse(r, files)

	if len(resp.IndirectCallers) != 1 || resp.IndirectCallers[0].Symbol != "Tier1Indirect" {
		t.Errorf("IndirectCallers = %+v, want [Tier1Indirect] (tier-1 only)", resp.IndirectCallers)
	}
	if resp.References.Count != 1 {
		t.Errorf("References.Count = %d, want 1 (Tier2Indirect in references)", resp.References.Count)
	}
	if len(resp.References.Examples) != 1 || resp.References.Examples[0].Symbol != "Tier2Indirect" {
		t.Errorf("References.Examples = %+v, want [Tier2Indirect]", resp.References.Examples)
	}
}

func TestBuildBlastResponseTier2ExamplesCap(t *testing.T) {
	var directCallers []model.Symbol
	tiers := make(map[int64]blast.Tier)
	for i := int64(1); i <= 8; i++ {
		directCallers = append(directCallers, model.Symbol{
			ID: i, Qualified: fmt.Sprintf("Ref%d", i), FileID: 100,
		})
		tiers[i] = blast.TierReferences
	}

	r := blast.Result{
		Symbol:        model.Symbol{ID: 0, Qualified: "Subject"},
		Risk:          blast.RiskLow,
		RiskReasons:   []string{"8 references"},
		DirectCallers: directCallers,
		AffectedTests: []string{},
		TotalAffected: 8,
		SymbolTiers:   tiers,
	}

	resp := BuildBlastResponse(r, noFiles)

	if len(resp.DirectCallers) != 0 {
		t.Errorf("DirectCallers = %d, want 0 (all tier-2)", len(resp.DirectCallers))
	}
	if resp.References.Count != 8 {
		t.Errorf("References.Count = %d, want 8", resp.References.Count)
	}
	if len(resp.References.Examples) != 5 {
		t.Errorf("References.Examples = %d, want 5 (capped at tier2ExamplesCap)", len(resp.References.Examples))
	}
}

func TestBuildBlastResponseSegmentTestFiles(t *testing.T) {
	r := blast.Result{
		Symbol:      model.Symbol{ID: 0, Qualified: "Subject"},
		Risk:        blast.RiskLow,
		RiskReasons: []string{"2 direct callers"},
		DirectCallers: []model.Symbol{
			{ID: 1, Qualified: "Prod", FileID: 10},
			{ID: 2, Qualified: "TestHelper", FileID: 11},
		},
		AffectedSubclasses: []model.Symbol{
			{ID: 3, Qualified: "TestSub", FileID: 12},
		},
		AffectedViaComposition: []model.Symbol{
			{ID: 4, Qualified: "TestComp", FileID: 13},
		},
		AffectedViaIncludes: []model.Symbol{
			{ID: 5, Qualified: "TestIncl", FileID: 14},
		},
		AffectedTests: []string{"spec/subject_spec.rb"},
		TotalAffected: 5,
		SymbolTiers: map[int64]blast.Tier{
			1: blast.TierBreaks,
			2: blast.TierBreaks,
			3: blast.TierReferences,
			4: blast.TierReferences,
			5: blast.TierReferences,
		},
	}

	files := func(id int64) (string, bool) {
		m := map[int64]string{
			10: "lib/prod.rb",
			11: "test/test_helper.rb",
			12: "test/sub_test.rb",
			13: "spec/comp_spec.rb",
			14: "spec/incl_spec.rb",
		}
		p, ok := m[id]
		return p, ok
	}

	resp := BuildBlastResponse(r, files)

	if resp.ProductionAffected != 1 {
		t.Errorf("ProductionAffected = %d, want 1 (only lib/prod.rb)", resp.ProductionAffected)
	}
	if resp.TestAffected != 5 {
		t.Errorf("TestAffected = %d, want 5 (test_helper + sub_test + comp_spec + incl_spec + affected_test)", resp.TestAffected)
	}
}

func TestBlastResponseRefField(t *testing.T) {
	r := blast.Result{
		Symbol:      model.Symbol{ID: 0, Qualified: "Subject"},
		Risk:        blast.RiskLow,
		RiskReasons: []string{"2 callers"},
		DirectCallers: []model.Symbol{
			{ID: 1, Qualified: "DirectCaller", FileID: 10, LineStart: 25},
			{ID: 2, Qualified: "NoFileCaller", FileID: 999, LineStart: 10},
		},
		IndirectCallers: []blast.CallerHop{
			{
				Symbol: model.Symbol{ID: 3, Qualified: "IndirectCaller", FileID: 11, LineStart: 42},
				Via:    model.Symbol{ID: 1, Qualified: "DirectCaller"},
				Hops:   2,
			},
		},
		AffectedTests: []string{},
		TotalAffected: 3,
	}

	files := func(id int64) (string, bool) {
		m := map[int64]string{10: "lib/caller.rb", 11: "lib/indirect.rb"}
		p, ok := m[id]
		return p, ok
	}

	resp := BuildBlastResponse(r, files)

	// DirectCallers — with file
	if resp.DirectCallers[0].Ref != "lib/caller.rb:25" {
		t.Errorf("DirectCallers[0].Ref = %q, want %q", resp.DirectCallers[0].Ref, "lib/caller.rb:25")
	}
	// DirectCallers — no file (lookup miss)
	if resp.DirectCallers[1].Ref != "" {
		t.Errorf("DirectCallers[1].Ref = %q, want empty (no file)", resp.DirectCallers[1].Ref)
	}

	// IndirectCallers
	if resp.IndirectCallers[0].Ref != "lib/indirect.rb:42" {
		t.Errorf("IndirectCallers[0].Ref = %q, want %q", resp.IndirectCallers[0].Ref, "lib/indirect.rb:42")
	}
}

func TestBlastDiffResponseRefField(t *testing.T) {
	sharedCaller := model.Symbol{ID: 42, Qualified: "Caller", FileID: 7, LineStart: 15}
	results := []blast.Result{
		{
			Symbol:        model.Symbol{ID: 1, Qualified: "A"},
			Risk:          blast.RiskLow,
			DirectCallers: []model.Symbol{sharedCaller},
			AffectedTests: []string{},
		},
	}

	files := func(id int64) (string, bool) {
		if id == 7 {
			return "lib/caller.rb", true
		}
		return "", false
	}

	resp := BuildDiffBlastResponse("HEAD~1", results, files)

	if len(resp.DirectCallers) != 1 {
		t.Fatalf("DirectCallers = %d, want 1", len(resp.DirectCallers))
	}
	if resp.DirectCallers[0].Ref != "lib/caller.rb:15" {
		t.Errorf("DirectCallers[0].Ref = %q, want %q", resp.DirectCallers[0].Ref, "lib/caller.rb:15")
	}
}

func TestBlastVerifyHintZeroAffectedWithCallees(t *testing.T) {
	r := blast.Result{
		Symbol:            model.Symbol{ID: 1, Qualified: "Unused"},
		Risk:              blast.RiskLow,
		RiskReasons:       []string{"0 direct callers"},
		DirectCallers:     []model.Symbol{},
		IndirectCallers:   []blast.CallerHop{},
		AffectedTests:     []string{},
		TotalAffected:     0,
		SubjectHasCallees: true,
	}

	resp := BuildBlastResponse(r, noFiles)

	if resp.VerifyHint == "" {
		t.Fatal("VerifyHint should be set when TotalAffected=0 and SubjectHasCallees=true")
	}
}

func TestBlastVerifyHintNotEmittedWithCallers(t *testing.T) {
	r := blast.Result{
		Symbol:            model.Symbol{ID: 1, Qualified: "Used"},
		Risk:              blast.RiskLow,
		RiskReasons:       []string{"1 direct caller"},
		DirectCallers:     []model.Symbol{{ID: 2, Qualified: "Caller", FileID: 10}},
		IndirectCallers:   []blast.CallerHop{},
		AffectedTests:     []string{},
		TotalAffected:     1,
		SubjectHasCallees: true,
	}

	resp := BuildBlastResponse(r, noFiles)

	if resp.VerifyHint != "" {
		t.Errorf("VerifyHint should be empty when TotalAffected > 0, got %q", resp.VerifyHint)
	}
}

func TestBlastVerifyHintNotEmittedLeafSymbol(t *testing.T) {
	r := blast.Result{
		Symbol:            model.Symbol{ID: 1, Qualified: "Leaf"},
		Risk:              blast.RiskLow,
		RiskReasons:       []string{"0 direct callers"},
		DirectCallers:     []model.Symbol{},
		IndirectCallers:   []blast.CallerHop{},
		AffectedTests:     []string{},
		TotalAffected:     0,
		SubjectHasCallees: false,
	}

	resp := BuildBlastResponse(r, noFiles)

	if resp.VerifyHint != "" {
		t.Errorf("VerifyHint should be empty for leaf symbol, got %q", resp.VerifyHint)
	}
}
