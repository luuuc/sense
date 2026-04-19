package mcpio

import (
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
