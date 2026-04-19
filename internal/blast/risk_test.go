package blast

import (
	"reflect"
	"testing"
)

// TestClassifyRiskThresholds pins every boundary of the pitch's
// three-tier formula: low ends at 2, medium starts at 3 and ends at
// 9, high starts at 10. One reason line always lands regardless of
// tier so consumers can format Reasons unconditionally.
func TestClassifyRiskThresholds(t *testing.T) {
	cases := []struct {
		directCallers int
		wantRisk      string
		wantReasons   []string
	}{
		{0, RiskLow, []string{"0 direct callers"}},
		{1, RiskLow, []string{"1 direct caller"}},
		{2, RiskLow, []string{"2 direct callers"}},
		{3, RiskMedium, []string{"3 direct callers"}},
		{5, RiskMedium, []string{"5 direct callers"}},
		{9, RiskMedium, []string{"9 direct callers"}},
		{10, RiskHigh, []string{"10 direct callers"}},
		{11, RiskHigh, []string{"11 direct callers"}},
		{100, RiskHigh, []string{"100 direct callers"}},
	}
	for _, c := range cases {
		gotRisk, gotReasons := classifyRisk(c.directCallers)
		if gotRisk != c.wantRisk {
			t.Errorf("classifyRisk(%d) risk = %q, want %q", c.directCallers, gotRisk, c.wantRisk)
		}
		if !reflect.DeepEqual(gotReasons, c.wantReasons) {
			t.Errorf("classifyRisk(%d) reasons = %v, want %v", c.directCallers, gotReasons, c.wantReasons)
		}
	}
}
