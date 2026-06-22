package mcpio

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/luuuc/sense/internal/blast"
	"github.com/luuuc/sense/internal/model"
)

// noFiles is a FileLookup that never resolves — the diff-union
// tests below exercise aggregation logic, not path hydration. Every
// BlastCaller.File will be "" under this lookup, which matches the
// documented "lookup miss → empty string" contract.
var noFiles FileLookup = func(int64) (string, bool) { return "", false }

func int64p(v int64) *int64 { return &v }

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
			resp := BuildDiffBlastResponse(context.Background(), "HEAD~1", results, noFiles, nil)
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

	resp := BuildDiffBlastResponse(context.Background(), "HEAD~1", results, noFiles, nil)
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

	resp := BuildBlastResponse(context.Background(), r, files, nil)

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

func TestBuildBlastResponseDirectEnumCap(t *testing.T) {
	// Build a Result with 250 Tier-1 (calls-edge) direct callers. The
	// enumerated direct_callers list is bounded at directEnumCap, but the
	// by-area map and total_affected preserve the true magnitude.
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

	resp := BuildBlastResponse(context.Background(), r, noFiles, nil)

	if len(resp.DirectCallers) != directEnumCap {
		t.Errorf("DirectCallers = %d, want %d (direct enum cap)", len(resp.DirectCallers), directEnumCap)
	}
	// by-area carries the full tier-1 direct count even though only the top
	// slice is enumerated. noFiles → all callers fall under the "." area.
	if got := sumAreas(resp.DirectCallersByArea); got != 250 {
		t.Errorf("by-area sum = %d, want 250 (full direct count preserved)", got)
	}
	if resp.TotalAffected != 250 {
		t.Errorf("TotalAffected = %d, want 250 (pre-cap count preserved)", resp.TotalAffected)
	}
}

func TestBuildBlastResponseByAreaTruthful(t *testing.T) {
	// 40 direct callers across three subsystems: 30 under app/models,
	// 7 under app/jobs, 3 under lib/foo. Only directEnumCap are enumerated
	// inline, but the by-area map must report all 40 grouped by directory so
	// the agent sees the structural shape and true total.
	type area struct {
		dir string
		n   int
	}
	areas := []area{{"app/models", 70}, {"app/jobs", 7}, {"lib/foo", 3}}
	var directCallers []model.Symbol
	tiers := make(map[int64]blast.Tier)
	paths := map[int64]string{}
	var id int64
	for _, a := range areas {
		for i := 0; i < a.n; i++ {
			id++
			directCallers = append(directCallers, model.Symbol{ID: id, Qualified: fmt.Sprintf("C%d", id), FileID: id})
			tiers[id] = blast.TierBreaks
			paths[id] = fmt.Sprintf("%s/file%d.rb", a.dir, i)
		}
	}
	files := func(fid int64) (string, bool) { p, ok := paths[fid]; return p, ok }

	r := blast.Result{
		Symbol:        model.Symbol{ID: 0, Qualified: "Subject"},
		DirectCallers: directCallers,
		AffectedTests: []string{},
		TotalAffected: 30,
		SymbolTiers:   tiers,
	}

	resp := BuildBlastResponse(context.Background(), r, files, nil)

	if len(resp.DirectCallers) != directEnumCap {
		t.Errorf("enumerated direct_callers = %d, want %d", len(resp.DirectCallers), directEnumCap)
	}
	want := map[string]int{"app/models": 70, "app/jobs": 7, "lib/foo": 3}
	for dir, n := range want {
		if resp.DirectCallersByArea[dir] != n {
			t.Errorf("by_area[%q] = %d, want %d", dir, resp.DirectCallersByArea[dir], n)
		}
	}
	if got := sumAreas(resp.DirectCallersByArea); got != 80 {
		t.Errorf("by_area sum = %d, want 80 (true direct count)", got)
	}
	if resp.Completeness == nil || resp.Completeness.Verdict != "partial" {
		t.Errorf("verdict = %v, want partial (more direct callers than enumerated)", resp.Completeness)
	}
}

func TestBuildBlastResponseEnumeratesHighestConfidence(t *testing.T) {
	// 80 tier-1 direct callers in ID order (> directEnumCap so the cap
	// bites), with confidence deliberately NOT monotonic in ID: caller i
	// gets confidence that peaks in the middle of the ID range. The
	// enumerated subset must be the highest-confidence callers (DESC),
	// tie-broken by ID ASC — NOT the lowest-ID prefix the engine's
	// determinism sort yields.
	const n = 80
	var directCallers []model.Symbol
	tiers := make(map[int64]blast.Tier)
	conf := map[int64]float64{}
	for i := int64(1); i <= n; i++ {
		directCallers = append(directCallers, model.Symbol{
			ID: i, Qualified: fmt.Sprintf("Caller%02d", i), FileID: 100,
		})
		tiers[i] = blast.TierBreaks
		// Tent function over [1,n]: confidence rises then falls, so the
		// top-confidence callers cluster around the middle IDs, far from
		// the lowest-ID prefix.
		if i <= n/2 {
			conf[i] = float64(i) / 100.0
		} else {
			conf[i] = float64(n-i+1) / 100.0
		}
	}

	r := blast.Result{
		Symbol:           model.Symbol{ID: 0, Qualified: "Subject"},
		DirectCallers:    directCallers,
		AffectedTests:    []string{},
		TotalAffected:    n,
		SymbolTiers:      tiers,
		DirectConfidence: conf,
	}

	resp := BuildBlastResponse(context.Background(), r, noFiles, nil)

	if len(resp.DirectCallers) != directEnumCap {
		t.Fatalf("enumerated direct_callers = %d, want %d", len(resp.DirectCallers), directEnumCap)
	}

	// Expected enumerated set: the directEnumCap highest-confidence IDs,
	// ordered by confidence DESC then ID ASC. Reconstruct independently.
	type rc struct {
		id   int64
		conf float64
	}
	var ranked []rc
	for id, c := range conf {
		ranked = append(ranked, rc{id, c})
	}
	sortRanked := func(a, b rc) bool {
		if a.conf != b.conf {
			return a.conf > b.conf
		}
		return a.id < b.id
	}
	// simple insertion sort to avoid pulling in sort just for the oracle
	for i := 1; i < len(ranked); i++ {
		for j := i; j > 0 && sortRanked(ranked[j], ranked[j-1]); j-- {
			ranked[j], ranked[j-1] = ranked[j-1], ranked[j]
		}
	}

	// The enumerated entries must be confidence-DESC (highest first) and
	// match the expected top-cap IDs.
	prev := 2.0
	for i, c := range resp.DirectCallers {
		gotConf := conf[symbolID(c.Symbol)]
		if gotConf > prev {
			t.Errorf("enumerated[%d] conf %.3f > previous %.3f — not confidence-descending", i, gotConf, prev)
		}
		prev = gotConf
		wantID := ranked[i].id
		if symbolID(c.Symbol) != wantID {
			t.Errorf("enumerated[%d] = %s (conf %.3f), want id %d (conf %.3f)", i, c.Symbol, gotConf, wantID, ranked[i].conf)
		}
	}

	// Determinism: a second build produces a byte-identical enumerated set.
	resp2 := BuildBlastResponse(context.Background(), r, noFiles, nil)
	if len(resp2.DirectCallers) != len(resp.DirectCallers) {
		t.Fatalf("non-deterministic length: %d vs %d", len(resp2.DirectCallers), len(resp.DirectCallers))
	}
	for i := range resp.DirectCallers {
		if resp.DirectCallers[i].Symbol != resp2.DirectCallers[i].Symbol {
			t.Errorf("non-deterministic at %d: %q vs %q", i, resp.DirectCallers[i].Symbol, resp2.DirectCallers[i].Symbol)
		}
	}

	// by_area sum and total stay truthful over the FULL set.
	if got := sumAreas(resp.DirectCallersByArea); got != n {
		t.Errorf("by_area sum = %d, want %d", got, n)
	}
	if resp.TotalAffected != n {
		t.Errorf("TotalAffected = %d, want %d", resp.TotalAffected, n)
	}
}

// TestBuildBlastResponseMoreAreasThanCap pins the behaviour when a symbol's
// callers span MORE distinct areas than directEnumCap. Round-robin can seat
// only one caller per area, so exactly directEnumCap areas get a citable
// exemplar (most-populated area first, name-tiebroken). The remaining areas
// are NOT hidden — every one still appears in direct_callers_by_area with
// its true count. This is the antirez/Kent "long tail" case: it must be
// summarised, never silently dropped.
func TestBuildBlastResponseMoreAreasThanCap(t *testing.T) {
	const nAreas = 70 // > directEnumCap
	var directCallers []model.Symbol
	tiers := make(map[int64]blast.Tier)
	paths := map[int64]string{}
	for i := int64(1); i <= nAreas; i++ {
		directCallers = append(directCallers, model.Symbol{ID: i, Qualified: fmt.Sprintf("C%d", i), FileID: i})
		tiers[i] = blast.TierBreaks
		paths[i] = fmt.Sprintf("area%03d/file.rb", i) // one caller, one area
	}
	files := func(fid int64) (string, bool) { p, ok := paths[fid]; return p, ok }

	r := blast.Result{
		Symbol:        model.Symbol{ID: 0, Qualified: "Subject"},
		DirectCallers: directCallers,
		AffectedTests: []string{},
		TotalAffected: nAreas,
		SymbolTiers:   tiers,
	}
	resp := BuildBlastResponse(context.Background(), r, files, nil)

	// Exactly cap callers, each from a distinct area (one-per-area: here one
	// file per area, so distinct files prove distinct areas).
	if len(resp.DirectCallers) != directEnumCap {
		t.Fatalf("enumerated = %d, want %d (cap)", len(resp.DirectCallers), directEnumCap)
	}
	seenFile := map[string]bool{}
	for _, c := range resp.DirectCallers {
		if seenFile[c.File] {
			t.Errorf("file %q enumerated twice — round-robin must seat one per area when areas exceed the cap", c.File)
		}
		seenFile[c.File] = true
	}
	// The un-enumerated tail is summarised, not hidden: by_area carries EVERY area.
	if len(resp.DirectCallersByArea) != nAreas {
		t.Errorf("by_area areas = %d, want %d (every area incl. the un-enumerated tail)", len(resp.DirectCallersByArea), nAreas)
	}
	if got := sumAreas(resp.DirectCallersByArea); got != nAreas {
		t.Errorf("by_area sum = %d, want %d (true direct count)", got, nAreas)
	}
	if resp.Completeness == nil || resp.Completeness.Verdict != "partial" {
		t.Errorf("verdict = %v, want partial (more areas than enumerated)", resp.Completeness)
	}

	// Deterministic: a second build yields the identical enumerated set.
	resp2 := BuildBlastResponse(context.Background(), r, files, nil)
	if len(resp2.DirectCallers) != len(resp.DirectCallers) {
		t.Fatalf("non-deterministic length: %d vs %d", len(resp2.DirectCallers), len(resp.DirectCallers))
	}
	for i := range resp.DirectCallers {
		if resp.DirectCallers[i].Symbol != resp2.DirectCallers[i].Symbol {
			t.Errorf("non-deterministic at %d: %q vs %q", i, resp.DirectCallers[i].Symbol, resp2.DirectCallers[i].Symbol)
		}
	}
}

// symbolID parses the trailing integer from a "CallerNN" qualified name,
// the oracle the confidence-ranking test uses to map a wire entry back to
// its fixture ID.
func symbolID(qualified string) int64 {
	var id int64
	// names are "CallerNN"; scan the digit suffix.
	for i := 0; i < len(qualified); i++ {
		if qualified[i] >= '0' && qualified[i] <= '9' {
			_, _ = fmt.Sscanf(qualified[i:], "%d", &id)
			break
		}
	}
	return id
}

func TestBuildBlastResponseEnumeratesBreadthAcrossAreas(t *testing.T) {
	// One big area swamps the ID range, plus many small scattered areas.
	// A flat conf-DESC/id-ASC rank (every conf 1.0 → pure id-ASC) would
	// fill the enumerated set entirely from the big low-ID area and crowd
	// out every scattered area. Breadth-first enumeration must surface a
	// citable exemplar from each scattered area before the big area takes a
	// second slot.
	bigArea := "app/models"
	scattered := []string{
		"lib/email", "lib/backup_restore", "lib/file_store",
		"lib/cooked", "lib/auth", "lib/jobs", "lib/search",
		"lib/tasks", "lib/validators", "lib/serializers",
	}

	var directCallers []model.Symbol
	tiers := make(map[int64]blast.Tier)
	conf := map[int64]float64{}
	paths := map[int64]string{}
	var id int64

	// 60 callers in the big area, all the lowest IDs (scan-order clustering).
	for i := 0; i < 60; i++ {
		id++
		directCallers = append(directCallers, model.Symbol{ID: id, Qualified: fmt.Sprintf("Big%d", id), FileID: id})
		tiers[id] = blast.TierBreaks
		conf[id] = 1.0
		paths[id] = fmt.Sprintf("%s/file%d.rb", bigArea, i)
	}
	// One caller in each scattered area, all at higher IDs.
	for _, area := range scattered {
		id++
		directCallers = append(directCallers, model.Symbol{ID: id, Qualified: fmt.Sprintf("S_%s_%d", area, id), FileID: id})
		tiers[id] = blast.TierBreaks
		conf[id] = 1.0
		paths[id] = fmt.Sprintf("%s/file.rb", area)
	}
	files := func(fid int64) (string, bool) { p, ok := paths[fid]; return p, ok }

	total := len(directCallers)
	r := blast.Result{
		Symbol:           model.Symbol{ID: 0, Qualified: "Upload"},
		DirectCallers:    directCallers,
		AffectedTests:    []string{},
		TotalAffected:    total,
		SymbolTiers:      tiers,
		DirectConfidence: conf,
	}

	resp := BuildBlastResponse(context.Background(), r, files, nil)

	if len(resp.DirectCallers) != directEnumCap {
		t.Fatalf("enumerated = %d, want %d", len(resp.DirectCallers), directEnumCap)
	}

	// (a) Breadth: EVERY scattered area must have a citable exemplar, and
	// the big area must not monopolize the enumerated set.
	enumAreas := map[string]int{}
	for _, c := range resp.DirectCallers {
		enumAreas[areaOf(c.File)]++
	}
	for _, area := range scattered {
		if enumAreas[area] == 0 {
			t.Errorf("scattered area %q has no enumerated exemplar (crowded out)", area)
		}
	}
	if enumAreas[bigArea] >= directEnumCap {
		t.Errorf("big area %q monopolized the enumerated set (%d of %d)", bigArea, enumAreas[bigArea], directEnumCap)
	}
	// 10 scattered areas each get a slot; the big area takes the remaining
	// 20. No single area exceeds the big area's share.
	if enumAreas[bigArea] != directEnumCap-len(scattered) {
		t.Errorf("big area got %d slots, want %d (cap minus scattered)", enumAreas[bigArea], directEnumCap-len(scattered))
	}

	// (d) by_area sum + total stay truthful over the FULL set.
	if got := sumAreas(resp.DirectCallersByArea); got != total {
		t.Errorf("by_area sum = %d, want %d", got, total)
	}
	if resp.TotalAffected != total {
		t.Errorf("TotalAffected = %d, want %d", resp.TotalAffected, total)
	}

	// (c) Determinism: a second build is identical entry-for-entry.
	resp2 := BuildBlastResponse(context.Background(), r, files, nil)
	if len(resp2.DirectCallers) != len(resp.DirectCallers) {
		t.Fatalf("non-deterministic length: %d vs %d", len(resp2.DirectCallers), len(resp.DirectCallers))
	}
	for i := range resp.DirectCallers {
		if resp.DirectCallers[i].Symbol != resp2.DirectCallers[i].Symbol {
			t.Errorf("non-deterministic at %d: %q vs %q", i, resp.DirectCallers[i].Symbol, resp2.DirectCallers[i].Symbol)
		}
	}
}

func TestBuildBlastResponseAreaExemplarIsHighestConfidence(t *testing.T) {
	// (b) Within an area, the chosen exemplar is the highest-confidence
	// caller, NOT the lowest-ID one. Two areas, each with three callers
	// whose top-confidence member is at the HIGHEST id (so id-ASC would
	// pick the wrong one). With cap=2, each area contributes exactly its
	// best exemplar.
	type caller struct {
		id   int64
		conf float64
		area string
	}
	callers := []caller{
		{1, 0.3, "app/a"}, {2, 0.5, "app/a"}, {3, 0.9, "app/a"}, // best is id 3
		{4, 0.4, "app/b"}, {5, 0.95, "app/b"}, {6, 0.6, "app/b"}, // best is id 5
	}

	// Drive enumerateByArea directly with a tight cap so each area yields
	// exactly its best exemplar.
	var ranked []rankedCaller
	for _, c := range callers {
		ranked = append(ranked, rankedCaller{
			entry: BlastCaller{Symbol: fmt.Sprintf("C%d", c.id)},
			area:  c.area, id: c.id, conf: c.conf,
		})
	}
	got := enumerateByArea(ranked, 2)
	gotSet := map[string]bool{}
	for _, e := range got {
		gotSet[e.Symbol] = true
	}
	if !gotSet["C3"] {
		t.Errorf("app/a exemplar should be C3 (conf 0.9), got %v", got)
	}
	if !gotSet["C5"] {
		t.Errorf("app/b exemplar should be C5 (conf 0.95), got %v", got)
	}
	if len(got) != 2 {
		t.Errorf("enumerated = %d, want 2 (one exemplar per area)", len(got))
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

	resp := BuildBlastResponse(context.Background(), r, noFiles, nil)

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

	resp := BuildBlastResponse(context.Background(), r, files, nil)

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

	resp := BuildBlastResponse(context.Background(), r, files, nil)

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

	resp := BuildBlastResponse(context.Background(), r, noFiles, nil)

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

	resp := BuildBlastResponse(context.Background(), r, files, nil)

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

	resp := BuildBlastResponse(context.Background(), r, files, nil)

	// Look the entries up by symbol — area-stratified enumeration clusters
	// by directory, so the Ref assertions must not assume input order.
	refBySymbol := map[string]string{}
	for _, c := range resp.DirectCallers {
		refBySymbol[c.Symbol] = c.Ref
	}
	// DirectCaller — with file
	if got := refBySymbol["DirectCaller"]; got != "lib/caller.rb:25" {
		t.Errorf("DirectCaller.Ref = %q, want %q", got, "lib/caller.rb:25")
	}
	// DirectCaller — no file (lookup miss)
	if got := refBySymbol["NoFileCaller"]; got != "" {
		t.Errorf("NoFileCaller.Ref = %q, want empty (no file)", got)
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

	resp := BuildDiffBlastResponse(context.Background(), "HEAD~1", results, files, nil)

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

	resp := BuildBlastResponse(context.Background(), r, noFiles, nil)

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

	resp := BuildBlastResponse(context.Background(), r, noFiles, nil)

	if resp.VerifyHint != "" {
		t.Errorf("VerifyHint should be empty when TotalAffected > 0, got %q", resp.VerifyHint)
	}
}

func TestBuildBlastResponseAffectedSymbolsAndFiles(t *testing.T) {
	r := blast.Result{
		Symbol:      model.Symbol{ID: 0, Qualified: "Subject"},
		Risk:        blast.RiskLow,
		RiskReasons: []string{"3 callers"},
		DirectCallers: []model.Symbol{
			{ID: 1, Qualified: "CallerA", FileID: 10, LineStart: 5},
			{ID: 2, Qualified: "CallerB", FileID: 11, LineStart: 10},
		},
		IndirectCallers: []blast.CallerHop{
			{
				Symbol: model.Symbol{ID: 3, Qualified: "IndirectC", FileID: 10, LineStart: 20},
				Via:    model.Symbol{ID: 1, Qualified: "CallerA"},
				Hops:   2,
			},
		},
		AffectedTests:  []string{"test/subject_test.rb"},
		TotalAffected:  3,
		EdgesTraversed: 47,
	}

	files := func(id int64) (string, bool) {
		m := map[int64]string{10: "lib/a.rb", 11: "lib/b.rb"}
		p, ok := m[id]
		return p, ok
	}

	resp := BuildBlastResponse(context.Background(), r, files, nil)

	if resp.AffectedSymbols != 3 {
		t.Errorf("AffectedSymbols = %d, want 3", resp.AffectedSymbols)
	}
	if resp.AffectedSymbols != resp.TotalAffected {
		t.Errorf("AffectedSymbols (%d) != TotalAffected (%d), should be aliases",
			resp.AffectedSymbols, resp.TotalAffected)
	}
	// lib/a.rb, lib/b.rb, test/subject_test.rb = 3 unique files
	if resp.AffectedFiles != 3 {
		t.Errorf("AffectedFiles = %d, want 3 (2 caller files + 1 test)", resp.AffectedFiles)
	}
	if resp.GraphEdgesTraversed != 47 {
		t.Errorf("GraphEdgesTraversed = %d, want 47", resp.GraphEdgesTraversed)
	}
}

func TestBuildDiffBlastResponseAffectedSymbolsAndFiles(t *testing.T) {
	results := []blast.Result{
		{
			Symbol:         model.Symbol{ID: 1, Qualified: "A"},
			Risk:           blast.RiskLow,
			DirectCallers:  []model.Symbol{{ID: 10, Qualified: "C1", FileID: 100}},
			AffectedTests:  []string{"test/a_test.rb"},
			EdgesTraversed: 20,
		},
		{
			Symbol:         model.Symbol{ID: 2, Qualified: "B"},
			Risk:           blast.RiskLow,
			DirectCallers:  []model.Symbol{{ID: 11, Qualified: "C2", FileID: 101}},
			AffectedTests:  []string{"test/b_test.rb"},
			EdgesTraversed: 15,
		},
	}

	files := func(id int64) (string, bool) {
		m := map[int64]string{100: "lib/c1.rb", 101: "lib/c2.rb"}
		p, ok := m[id]
		return p, ok
	}

	resp := BuildDiffBlastResponse(context.Background(), "HEAD~1", results, files, nil)

	if resp.AffectedSymbols != 2 {
		t.Errorf("AffectedSymbols = %d, want 2", resp.AffectedSymbols)
	}
	if resp.AffectedSymbols != resp.TotalAffected {
		t.Errorf("AffectedSymbols (%d) != TotalAffected (%d)", resp.AffectedSymbols, resp.TotalAffected)
	}
	// lib/c1.rb, lib/c2.rb, test/a_test.rb, test/b_test.rb = 4
	if resp.AffectedFiles != 4 {
		t.Errorf("AffectedFiles = %d, want 4", resp.AffectedFiles)
	}
	if resp.GraphEdgesTraversed != 35 {
		t.Errorf("GraphEdgesTraversed = %d, want 35 (20+15)", resp.GraphEdgesTraversed)
	}
}

// TestBuildDiffBlastResponseTestsAffectedCount pins that the diff path
// populates tests_affected_count from the deduped affected_tests list —
// it was silently left at 0 while the list held entries, contradicting
// the data a consumer reads for safe-to-change decisions.
func TestBuildDiffBlastResponseTestsAffectedCount(t *testing.T) {
	results := []blast.Result{
		{
			Symbol:        model.Symbol{ID: 1, Qualified: "A"},
			Risk:          blast.RiskLow,
			AffectedTests: []string{"test/a_test.rb", "test/shared_test.rb"},
		},
		{
			Symbol:        model.Symbol{ID: 2, Qualified: "B"},
			Risk:          blast.RiskLow,
			AffectedTests: []string{"test/b_test.rb", "test/shared_test.rb"},
		},
	}

	resp := BuildDiffBlastResponse(context.Background(), "HEAD~1", results, noFiles, nil)

	// shared_test.rb is deduped → 3 unique tests.
	if len(resp.AffectedTests) != 3 {
		t.Fatalf("AffectedTests = %d, want 3", len(resp.AffectedTests))
	}
	if resp.TestsAffectedCount != len(resp.AffectedTests) {
		t.Errorf("TestsAffectedCount = %d, want %d (must match affected_tests)",
			resp.TestsAffectedCount, len(resp.AffectedTests))
	}
}

func bigBlastResponse(direct, indirect, tests int) BlastResponse {
	r := BlastResponse{
		Symbol:             "Hub",
		Risk:               "high",
		TotalAffected:      direct + indirect,
		AffectedSymbols:    direct + indirect,
		TestsAffectedCount: tests,
	}
	for i := 0; i < direct; i++ {
		r.DirectCallers = append(r.DirectCallers, BlastCaller{
			Symbol: "pkg.Caller" + itoa(i), File: "app/models/caller" + itoa(i) + ".rb", LineStart: i,
			CallSite: &CallSite{Line: i, Snippet: "some representative source line of code here"},
		})
	}
	for i := 0; i < indirect; i++ {
		r.IndirectCallers = append(r.IndirectCallers, BlastIndirect{Symbol: "pkg.Ind" + itoa(i), Via: "pkg.V", Hops: 2})
	}
	for i := 0; i < tests; i++ {
		r.AffectedTests = append(r.AffectedTests, "test/thing"+itoa(i)+"_test.rb")
	}
	return r
}

func itoa(i int) string { return fmt.Sprintf("%d", i) }

func TestApplyBlastBudgetNoopUnderBudget(t *testing.T) {
	r := bigBlastResponse(5, 3, 2)
	ApplyBlastBudget(&r, 1_000_000)
	if r.Truncated || len(r.DirectCallers) != 5 || len(r.IndirectCallers) != 3 || len(r.AffectedTests) != 2 {
		t.Errorf("under-budget response should be untouched: %+v", r)
	}
}

func TestApplyBlastBudgetDisabledWhenNonPositive(t *testing.T) {
	r := bigBlastResponse(200, 200, 200)
	ApplyBlastBudget(&r, 0)
	if r.Truncated || len(r.DirectCallers) != 200 {
		t.Error("budget <= 0 must disable trimming")
	}
}

func TestApplyBlastBudgetTrimsToFitPreservingCounts(t *testing.T) {
	r := bigBlastResponse(400, 300, 120)
	const budget = 1500
	ApplyBlastBudget(&r, budget)

	if got := estimateJSONTokens(&r); got > budget {
		t.Errorf("after trim, tokens=%d still exceed budget=%d", got, budget)
	}
	if !r.Truncated {
		t.Error("expected Truncated=true after trimming")
	}
	// Summary counts must survive untouched — the headline answer stays true.
	if r.TotalAffected != 700 || r.TestsAffectedCount != 120 {
		t.Errorf("counts must not shrink: TotalAffected=%d TestsAffectedCount=%d", r.TotalAffected, r.TestsAffectedCount)
	}
	if len(r.DirectCallers) < 1 {
		t.Error("must keep at least one direct caller")
	}
	if len(r.AffectedTests) > blastTestExamplesCap {
		t.Errorf("affected_tests sample = %d, want <= %d", len(r.AffectedTests), blastTestExamplesCap)
	}
}

func TestTrimStep(t *testing.T) {
	cases := map[int]int{1: 1, 5: 1, 9: 1, 10: 1, 20: 2, 100: 10}
	for n, want := range cases {
		if got := trimStep(n); got != want {
			t.Errorf("trimStep(%d) = %d, want %d", n, got, want)
		}
	}
}

func TestBuildBlastResponseZeroEdges(t *testing.T) {
	r := blast.Result{
		Symbol:         model.Symbol{ID: 1, Qualified: "Isolated"},
		Risk:           blast.RiskLow,
		RiskReasons:    []string{"0 direct callers"},
		DirectCallers:  []model.Symbol{},
		AffectedTests:  []string{},
		TotalAffected:  0,
		EdgesTraversed: 0,
	}

	resp := BuildBlastResponse(context.Background(), r, noFiles, nil)

	if resp.AffectedSymbols != 0 {
		t.Errorf("AffectedSymbols = %d, want 0", resp.AffectedSymbols)
	}
	if resp.AffectedFiles != 0 {
		t.Errorf("AffectedFiles = %d, want 0", resp.AffectedFiles)
	}
	if resp.GraphEdgesTraversed != 0 {
		t.Errorf("GraphEdgesTraversed = %d, want 0", resp.GraphEdgesTraversed)
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

	resp := BuildBlastResponse(context.Background(), r, noFiles, nil)

	if resp.VerifyHint != "" {
		t.Errorf("VerifyHint should be empty for leaf symbol, got %q", resp.VerifyHint)
	}
}

func TestBuildBlastResponseCallSiteSnippets(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "caller.go", "package main\n\nfunc caller() {\n\ttarget()\n\treturn\n}\n")

	line := 4
	filePaths := map[int64]string{
		1: "target.go",
		2: "caller.go",
	}
	files := func(id int64) (string, bool) {
		p, ok := filePaths[id]
		return p, ok
	}

	r := blast.Result{
		Symbol: model.Symbol{Name: "target", Qualified: "target"},
		Risk:   blast.RiskLow,
		DirectCallers: []model.Symbol{
			{ID: 10, Name: "caller", Qualified: "caller", FileID: 2, LineStart: 3, LineEnd: 6},
		},
		DirectEdgeSites: map[int64]blast.EdgeSite{
			10: {FileID: int64p(2), Line: &line},
		},
		AffectedTests: []string{},
		RiskReasons:   []string{},
	}

	snippets := NewSnippetReader(dir, 2)
	resp := BuildBlastResponse(context.Background(), r, files, snippets)

	if len(resp.DirectCallers) != 1 {
		t.Fatalf("DirectCallers = %d, want 1", len(resp.DirectCallers))
	}
	c := resp.DirectCallers[0]
	if c.CallSite == nil {
		t.Fatal("CallSite is nil, want non-nil")
	}
	if c.CallSite.Line != 4 {
		t.Errorf("CallSite.Line = %d, want 4", c.CallSite.Line)
	}
	lines := splitLines(c.CallSite.Snippet)
	if len(lines) != 5 {
		t.Errorf("snippet lines = %d, want 5; snippet:\n%s", len(lines), c.CallSite.Snippet)
	}
}

func TestBuildBlastResponseCallSiteZeroSuppresses(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "caller.go", "package main\n\nfunc caller() {\n\ttarget()\n}\n")

	line := 4
	files := func(id int64) (string, bool) {
		if id == 2 {
			return "caller.go", true
		}
		return "", false
	}

	r := blast.Result{
		Symbol: model.Symbol{Name: "target", Qualified: "target"},
		Risk:   blast.RiskLow,
		DirectCallers: []model.Symbol{
			{ID: 10, Name: "caller", Qualified: "caller", FileID: 2, LineStart: 3},
		},
		DirectEdgeSites: map[int64]blast.EdgeSite{
			10: {FileID: int64p(2), Line: &line},
		},
		AffectedTests: []string{},
		RiskReasons:   []string{},
	}

	snippets := NewSnippetReader(dir, 0)
	resp := BuildBlastResponse(context.Background(), r, files, snippets)

	if resp.DirectCallers[0].CallSite != nil {
		t.Error("CallSite should be nil when context_lines=0")
	}
}

func TestBuildBlastResponseCallSiteMissingEdgeSite(t *testing.T) {
	files := func(_ int64) (string, bool) { return "x.go", true }

	r := blast.Result{
		Symbol: model.Symbol{Name: "target", Qualified: "target"},
		Risk:   blast.RiskLow,
		DirectCallers: []model.Symbol{
			{ID: 10, Name: "caller", Qualified: "caller", FileID: 1, LineStart: 3},
		},
		AffectedTests: []string{},
		RiskReasons:   []string{},
	}

	snippets := NewSnippetReader(t.TempDir(), 2)
	resp := BuildBlastResponse(context.Background(), r, files, snippets)

	if resp.DirectCallers[0].CallSite != nil {
		t.Error("CallSite should be nil when no edge site exists")
	}
}

func TestBuildBlastResponseCallSiteEdgeFileMissing(t *testing.T) {
	noFiles := func(int64) (string, bool) { return "", false }

	line := 4
	r := blast.Result{
		Symbol: model.Symbol{Name: "target", Qualified: "target"},
		Risk:   blast.RiskLow,
		DirectCallers: []model.Symbol{
			{ID: 10, Name: "caller", Qualified: "caller", FileID: 2, LineStart: 3},
		},
		DirectEdgeSites: map[int64]blast.EdgeSite{
			10: {FileID: int64p(99), Line: &line},
		},
		AffectedTests: []string{},
		RiskReasons:   []string{},
	}

	snippets := NewSnippetReader(t.TempDir(), 2)
	resp := BuildBlastResponse(context.Background(), r, noFiles, snippets)

	if resp.DirectCallers[0].CallSite != nil {
		t.Error("CallSite should be nil when edge file not in lookup")
	}
}

func TestBuildDiffBlastResponseCallSiteSnippets(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "caller.go", "package main\n\nfunc caller() {\n\ttarget()\n\treturn\n}\n")

	line := 4
	filePaths := map[int64]string{
		1: "target.go",
		2: "caller.go",
	}
	files := func(id int64) (string, bool) {
		p, ok := filePaths[id]
		return p, ok
	}

	results := []blast.Result{
		{
			Symbol: model.Symbol{ID: 1, Name: "target", Qualified: "target"},
			Risk:   blast.RiskLow,
			DirectCallers: []model.Symbol{
				{ID: 10, Name: "caller", Qualified: "caller", FileID: 2, LineStart: 3, LineEnd: 6},
			},
			DirectEdgeSites: map[int64]blast.EdgeSite{
				10: {FileID: int64p(2), Line: &line},
			},
			AffectedTests: []string{},
		},
	}

	snippets := NewSnippetReader(dir, 2)
	resp := BuildDiffBlastResponse(context.Background(), "HEAD~1", results, files, snippets)

	if len(resp.DirectCallers) != 1 {
		t.Fatalf("DirectCallers = %d, want 1", len(resp.DirectCallers))
	}
	c := resp.DirectCallers[0]
	if c.CallSite == nil {
		t.Fatal("CallSite is nil in diff blast, want non-nil")
	}
	if c.CallSite.Line != 4 {
		t.Errorf("CallSite.Line = %d, want 4", c.CallSite.Line)
	}
}

func TestBuildBlastResponseSnippetsTruncated(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "caller.go", "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n13\n14\n15\n")

	files := func(_ int64) (string, bool) { return "caller.go", true }

	var callers []model.Symbol
	edgeSites := map[int64]blast.EdgeSite{}
	for i := 0; i < 15; i++ {
		line := i + 1
		callers = append(callers, model.Symbol{
			ID: int64(i + 10), Name: "c", Qualified: "c", FileID: 1, LineStart: line,
		})
		edgeSites[int64(i+10)] = blast.EdgeSite{FileID: int64p(1), Line: &line}
	}

	r := blast.Result{
		Symbol:          model.Symbol{Name: "target", Qualified: "target"},
		Risk:            blast.RiskLow,
		DirectCallers:   callers,
		DirectEdgeSites: edgeSites,
		AffectedTests:   []string{},
		RiskReasons:     []string{},
	}

	snippets := NewSnippetReader(dir, 1)
	resp := BuildBlastResponse(context.Background(), r, files, snippets)

	if !resp.SnippetsTruncated {
		t.Error("SnippetsTruncated should be true")
	}

	withSnippet := 0
	for _, c := range resp.DirectCallers {
		if c.CallSite != nil {
			withSnippet++
		}
	}
	if withSnippet != SnippetCap {
		t.Errorf("snippets with content = %d, want %d", withSnippet, SnippetCap)
	}
}

func TestSegmentBlastCallers(t *testing.T) {
	resp := &BlastResponse{
		DirectCallers: []BlastCaller{
			{File: "app/service.go"},
			{File: "app/service_test.go"},
		},
		IndirectCallers: []BlastIndirect{
			{Symbol: "helper", Via: "service", Hops: 2},
		},
		AffectedSubclasses: []BlastCaller{
			{File: "test/sub_test.go"},
		},
		AffectedViaComposition: []BlastCaller{
			{File: "app/composer.go"},
		},
		AffectedViaIncludes: []BlastCaller{
			{File: "test/includes_test.go"},
		},
		AffectedTests: []string{
			"test/something_test.go",
		},
	}

	segmentBlastCallers(resp)

	// Prod: service.go (direct) + 1 indirect + composer.go (composition) = 3
	// Test: service_test.go (direct) + sub_test.go (subclass) + includes_test.go (includes) + something_test.go (tests) = 4
	if resp.ProductionAffected != 3 {
		t.Errorf("ProductionAffected = %d, want 3", resp.ProductionAffected)
	}
	if resp.TestAffected != 4 {
		t.Errorf("TestAffected = %d, want 4", resp.TestAffected)
	}
}

func TestRiskRank(t *testing.T) {
	cases := []struct {
		risk string
		want int
	}{
		{"high", 3},
		{"medium", 2},
		{"low", 1},
		{"unknown", 0},
		{"", 0},
	}
	for _, c := range cases {
		if got := riskRank(c.risk); got != c.want {
			t.Errorf("riskRank(%q) = %d, want %d", c.risk, got, c.want)
		}
	}
}

// --- Per-session seen-caller deduplication (option B) -----------------------

// seenSet is a SeenFunc backed by a set literal — the test-side stand-in for
// the handler's per-session seenSymbols map.
func seenSet(ids ...int64) SeenFunc {
	m := map[int64]bool{}
	for _, id := range ids {
		m[id] = true
	}
	return func(id int64) bool { return m[id] }
}

// blastFixture builds a small high-tier blast.Result with N direct callers,
// one per area, so each enumerated caller maps to a distinct file/area.
func seenBlastFixture(n int) (blast.Result, FileLookup) {
	var callers []model.Symbol
	tiers := map[int64]blast.Tier{}
	conf := map[int64]float64{}
	paths := map[int64]string{}
	for i := int64(1); i <= int64(n); i++ {
		callers = append(callers, model.Symbol{ID: i, Qualified: fmt.Sprintf("C%d", i), FileID: i})
		tiers[i] = blast.TierBreaks
		conf[i] = 1.0
		paths[i] = fmt.Sprintf("area%03d/file.rb", i)
	}
	r := blast.Result{
		Symbol:           model.Symbol{ID: 0, Qualified: "Subject"},
		DirectCallers:    callers,
		AffectedTests:    []string{},
		TotalAffected:    n,
		SymbolTiers:      tiers,
		DirectConfidence: conf,
		Risk:             blast.RiskLow,
	}
	files := func(fid int64) (string, bool) { p, ok := paths[fid]; return p, ok }
	return r, files
}

// TestBuildBlastResponseSeenNilIsByteIdentical pins the single-call-safe
// invariant: a nil SeenFunc (CLI path, empty seen-set) collapses nothing, so
// the response equals the un-deduplicated build exactly.
func TestBuildBlastResponseSeenNilIsByteIdentical(t *testing.T) {
	r, files := seenBlastFixture(5)
	plain := BuildBlastResponse(context.Background(), r, files, nil)
	seenNil := BuildBlastResponseSeen(context.Background(), r, files, nil, nil)

	if seenNil.SeenVia != nil {
		t.Errorf("nil seen-set must not set seen_elsewhere, got %+v", seenNil.SeenVia)
	}
	pj, _ := json.Marshal(plain)
	sj, _ := json.Marshal(seenNil)
	if string(pj) != string(sj) {
		t.Errorf("nil SeenFunc must be byte-identical to BuildBlastResponse\nplain: %s\nseen:  %s", pj, sj)
	}
}

// TestBuildBlastResponseSeenCollapsesPerID pins that ONLY the specific seen
// ids are collapsed — unseen callers stay fully enumerated, and the collapsed
// count is summarised in seen_elsewhere.
func TestBuildBlastResponseSeenCollapsesPerID(t *testing.T) {
	r, files := seenBlastFixture(5)
	// Mark 3 of the 5 direct callers as already returned this session.
	resp := BuildBlastResponseSeen(context.Background(), r, files, nil, seenSet(1, 2, 3))

	if len(resp.DirectCallers) != 2 {
		t.Errorf("enumerated direct_callers = %d, want 2 (only the 2 unseen)", len(resp.DirectCallers))
	}
	for _, c := range resp.DirectCallers {
		if id := symbolID(c.Symbol); id == 1 || id == 2 || id == 3 {
			t.Errorf("seen caller C%d must not be enumerated", id)
		}
	}
	if resp.SeenVia == nil || resp.SeenVia.Count != 3 {
		t.Fatalf("seen_elsewhere = %+v, want count 3", resp.SeenVia)
	}
	if !strings.Contains(resp.SeenVia.Note, "3 of 5") {
		t.Errorf("seen_elsewhere note = %q, want it to mention 3 of 5", resp.SeenVia.Note)
	}
}

// TestBuildBlastResponseSeenPreservesMagnitude pins that the collapse leaves
// total_affected, affected_symbols, affected_files, and direct_callers_by_area
// reporting the TRUE full set — the by-area map is computed before collapse.
func TestBuildBlastResponseSeenPreservesMagnitude(t *testing.T) {
	r, files := seenBlastFixture(5)
	full := BuildBlastResponse(context.Background(), r, files, nil)
	collapsed := BuildBlastResponseSeen(context.Background(), r, files, nil, seenSet(1, 2, 3))

	if collapsed.TotalAffected != full.TotalAffected {
		t.Errorf("total_affected = %d, want %d (unchanged by collapse)", collapsed.TotalAffected, full.TotalAffected)
	}
	if collapsed.AffectedSymbols != full.AffectedSymbols {
		t.Errorf("affected_symbols = %d, want %d", collapsed.AffectedSymbols, full.AffectedSymbols)
	}
	if collapsed.AffectedFiles != full.AffectedFiles {
		t.Errorf("affected_files = %d, want %d (collapsed callers' files still counted)", collapsed.AffectedFiles, full.AffectedFiles)
	}
	if sumAreas(collapsed.DirectCallersByArea) != sumAreas(full.DirectCallersByArea) {
		t.Errorf("by_area sum = %d, want %d (full set, computed before collapse)",
			sumAreas(collapsed.DirectCallersByArea), sumAreas(full.DirectCallersByArea))
	}
	if len(collapsed.DirectCallersByArea) != len(full.DirectCallersByArea) {
		t.Errorf("by_area areas = %d, want %d", len(collapsed.DirectCallersByArea), len(full.DirectCallersByArea))
	}
}

// TestBuildBlastResponseSeenKeepsCompleteVerdict pins the load-bearing
// invariant: collapsing already-seen callers is a dedup, not a truncation, so
// a response that was "complete" stays "complete" even when every direct
// caller is collapsed.
func TestBuildBlastResponseSeenKeepsCompleteVerdict(t *testing.T) {
	r, files := seenBlastFixture(5)
	// Without collapse this fixture is complete (5 callers, all enumerated).
	full := BuildBlastResponse(context.Background(), r, files, nil)
	if full.Completeness == nil || full.Completeness.Verdict != "complete" {
		t.Fatalf("precondition: full build must be complete, got %+v", full.Completeness)
	}

	// Collapse ALL five — the agent already holds them from the prior call.
	resp := BuildBlastResponseSeen(context.Background(), r, files, nil, seenSet(1, 2, 3, 4, 5))
	if len(resp.DirectCallers) != 0 {
		t.Fatalf("all callers seen, enumerated should be 0, got %d", len(resp.DirectCallers))
	}
	if resp.Completeness == nil || resp.Completeness.Verdict != "complete" {
		t.Errorf("verdict = %+v, want complete (seen-collapse is not truncation)", resp.Completeness)
	}
	if resp.Truncated {
		t.Error("Truncated must stay false — collapse is not a budget trim")
	}
	if resp.Completeness.Hidden != 0 {
		t.Errorf("hidden = %d, want 0 (collapsed callers are in the agent's context, not hidden)", resp.Completeness.Hidden)
	}
}

// TestBuildBlastResponseSeenLeavesNonCallerSetsAlone pins that the collapse
// never touches the inherit/include/compose affected sets or indirect callers:
// those may NOT have appeared in the prior graph response, so they stay fully
// enumerated even when their ids coincide with the seen set.
func TestBuildBlastResponseSeenLeavesNonCallerSetsAlone(t *testing.T) {
	r := blast.Result{
		Symbol:        model.Symbol{ID: 0, Qualified: "Subject"},
		DirectCallers: []model.Symbol{{ID: 1, Qualified: "C1", FileID: 1}},
		IndirectCallers: []blast.CallerHop{
			{Symbol: model.Symbol{ID: 2, Qualified: "I2", FileID: 2}, Via: model.Symbol{ID: 1, Qualified: "C1"}, Hops: 2},
		},
		AffectedSubclasses:     []model.Symbol{{ID: 3, Qualified: "Sub3", FileID: 3}},
		AffectedViaComposition: []model.Symbol{{ID: 4, Qualified: "Comp4", FileID: 4}},
		AffectedViaIncludes:    []model.Symbol{{ID: 5, Qualified: "Inc5", FileID: 5}},
		AffectedTests:          []string{},
		TotalAffected:          2, // direct + indirect union
	}
	// Mark every id seen — even the indirect/subclass/compose/include ids.
	resp := BuildBlastResponseSeen(context.Background(), r, noFiles, nil, seenSet(1, 2, 3, 4, 5))

	if len(resp.DirectCallers) != 0 || resp.SeenVia == nil || resp.SeenVia.Count != 1 {
		t.Errorf("direct caller C1 should collapse: callers=%d seen=%+v", len(resp.DirectCallers), resp.SeenVia)
	}
	// Only DIRECT callers collapse; the other relations are untouched.
	if len(resp.IndirectCallers) != 1 {
		t.Errorf("indirect_callers = %d, want 1 (never collapsed)", len(resp.IndirectCallers))
	}
	if len(resp.AffectedSubclasses) != 1 {
		t.Errorf("affected_subclasses = %d, want 1 (never collapsed)", len(resp.AffectedSubclasses))
	}
	if len(resp.AffectedViaComposition) != 1 {
		t.Errorf("affected_via_composition = %d, want 1 (never collapsed)", len(resp.AffectedViaComposition))
	}
	if len(resp.AffectedViaIncludes) != 1 {
		t.Errorf("affected_via_includes = %d, want 1 (never collapsed)", len(resp.AffectedViaIncludes))
	}
}

// TestBuildDiffBlastResponseSeenCollapses pins the same dedup on the diff
// builder: a seen direct caller is collapsed, magnitude (total_affected, the
// risk-factor count) reflects the full deduplicated union, not the remainder.
func TestBuildDiffBlastResponseSeenCollapses(t *testing.T) {
	results := []blast.Result{
		{
			Symbol: model.Symbol{ID: 1, Qualified: "A"},
			Risk:   blast.RiskLow,
			DirectCallers: []model.Symbol{
				{ID: 10, Qualified: "Seen", FileID: 1},
				{ID: 11, Qualified: "New", FileID: 2},
			},
			AffectedTests: []string{},
		},
	}
	resp := BuildDiffBlastResponseSeen(context.Background(), "HEAD~1", results, noFiles, nil, seenSet(10))

	if len(resp.DirectCallers) != 1 || resp.DirectCallers[0].Symbol != "New" {
		t.Errorf("direct_callers = %+v, want only the unseen 'New'", resp.DirectCallers)
	}
	if resp.SeenVia == nil || resp.SeenVia.Count != 1 {
		t.Fatalf("seen_elsewhere = %+v, want count 1", resp.SeenVia)
	}
	if resp.TotalAffected != 2 {
		t.Errorf("total_affected = %d, want 2 (full dedup union, not the remainder)", resp.TotalAffected)
	}
	if !strings.Contains(resp.RiskFactors[0], "2 direct callers") {
		t.Errorf("risk factor = %q, want it to report 2 direct callers (full magnitude)", resp.RiskFactors[0])
	}
}
