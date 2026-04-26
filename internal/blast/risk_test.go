package blast

import (
	"reflect"
	"testing"
)

func TestClassifyRiskThresholds(t *testing.T) {
	cases := []struct {
		directCallers int
		hasTemporal   bool
		wantRisk      string
		wantReasons   []string
	}{
		{0, false, RiskLow, []string{"0 direct callers"}},
		{1, false, RiskLow, []string{"1 direct caller"}},
		{2, false, RiskLow, []string{"2 direct callers"}},
		{3, false, RiskMedium, []string{"3 direct callers"}},
		{5, false, RiskMedium, []string{"5 direct callers"}},
		{9, false, RiskMedium, []string{"9 direct callers"}},
		{10, false, RiskHigh, []string{"10 direct callers"}},
		{11, false, RiskHigh, []string{"11 direct callers"}},
		{100, false, RiskHigh, []string{"100 direct callers"}},
	}
	for _, c := range cases {
		gotRisk, gotReasons := classifyRisk(c.directCallers, c.hasTemporal)
		if gotRisk != c.wantRisk {
			t.Errorf("classifyRisk(%d, %v) risk = %q, want %q", c.directCallers, c.hasTemporal, gotRisk, c.wantRisk)
		}
		if !reflect.DeepEqual(gotReasons, c.wantReasons) {
			t.Errorf("classifyRisk(%d, %v) reasons = %v, want %v", c.directCallers, c.hasTemporal, gotReasons, c.wantReasons)
		}
	}
}

func TestClassifyRiskTemporalBump(t *testing.T) {
	risk, reasons := classifyRisk(0, true)
	if risk != RiskMedium {
		t.Errorf("expected medium with 0 callers + temporal, got %q", risk)
	}
	if len(reasons) != 2 {
		t.Fatalf("expected 2 reasons, got %d: %v", len(reasons), reasons)
	}
	if reasons[0] != "0 direct callers" {
		t.Errorf("reasons[0] = %q, want %q", reasons[0], "0 direct callers")
	}

	// Temporal + already medium stays medium.
	risk, _ = classifyRisk(3, true)
	if risk != RiskMedium {
		t.Errorf("expected medium with 3 callers + temporal, got %q", risk)
	}

	// Temporal + already high stays high.
	risk, _ = classifyRisk(10, true)
	if risk != RiskHigh {
		t.Errorf("expected high with 10 callers + temporal, got %q", risk)
	}
}
