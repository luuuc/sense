package mcpio

// Wire-shape pins for the retained_via_interfaces group (pitch 31-12): the
// group is omitted entirely when empty — byte-identity for languages without
// interface symbols depends on ALL THREE keys (list, count, note) vanishing
// from the marshaled response, not serializing as empty values.

import (
	"context"
	"encoding/json"
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
