package mcpio

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/luuuc/sense/internal/dead"
)

// finding is a small constructor for test inputs.
func finding(qualified, file string, line int, kind string, verdict dead.Verdict, reasonCode, hint string) dead.Finding {
	f := dead.Finding{
		Symbol: dead.Symbol{
			Qualified: qualified,
			Name:      lastSeg(qualified),
			File:      file,
			LineStart: line,
			Kind:      kind,
		},
		Verdict: verdict,
	}
	if verdict == dead.VerdictPossiblyDead {
		f.Reason = &dead.Reason{Code: reasonCode, Hint: hint}
	}
	return f
}

func lastSeg(q string) string {
	if i := strings.LastIndexAny(q, "#."); i >= 0 {
		return q[i+1:]
	}
	return q
}

// TestBuildSplitsDeadAndPossiblyDead pins the core split: dead findings land
// in the dead list, possibly_dead findings group by reason.
func TestBuildSplitsDeadAndPossiblyDead(t *testing.T) {
	findings := []dead.Finding{
		finding("A#dead1", "a.rb", 10, "method", dead.VerdictDead, "", ""),
		finding("B#pd1", "b.rb", 20, "method", dead.VerdictPossiblyDead, dead.ReasonReflection, "reflective"),
		finding("B#pd2", "b.rb", 30, "method", dead.VerdictPossiblyDead, dead.ReasonReflection, "reflective"),
	}
	resp := BuildUnreferencedResponse(findings, 100, 0)

	if resp.DeadCount != 1 {
		t.Errorf("DeadCount = %d, want 1", resp.DeadCount)
	}
	if got := len(resp.Unreferenced.Dead); got != 1 {
		t.Fatalf("dead entries = %d, want 1", got)
	}
	if resp.Unreferenced.Dead[0].Qualified != "A#dead1" {
		t.Errorf("dead[0] = %q, want A#dead1", resp.Unreferenced.Dead[0].Qualified)
	}
	if resp.PossiblyDeadCount != 2 {
		t.Errorf("PossiblyDeadCount = %d, want 2", resp.PossiblyDeadCount)
	}
	if got := len(resp.Unreferenced.PossiblyDead); got != 1 {
		t.Fatalf("possibly_dead groups = %d, want 1 (both share a reason)", got)
	}
	if got := len(resp.Unreferenced.PossiblyDead[0].Symbols); got != 2 {
		t.Errorf("group symbols = %d, want 2", got)
	}
}

// TestBuildGroupsRankedByPriority pins customer constraint "rank by
// actionability": groups order by reason priority descending. ExportedAPI
// (50) outranks Reflection (30).
func TestBuildGroupsRankedByPriority(t *testing.T) {
	findings := []dead.Finding{
		finding("R#r", "r.rb", 1, "method", dead.VerdictPossiblyDead, dead.ReasonReflection, "h"),
		finding("E#e", "e.rb", 1, "method", dead.VerdictPossiblyDead, dead.ReasonExportedAPI, "h"),
	}
	resp := BuildUnreferencedResponse(findings, 10, 0)
	groups := resp.Unreferenced.PossiblyDead
	if len(groups) != 2 {
		t.Fatalf("groups = %d, want 2", len(groups))
	}
	if groups[0].Reason.Code != dead.ReasonExportedAPI {
		t.Errorf("first group = %q, want %q (higher priority)", groups[0].Reason.Code, dead.ReasonExportedAPI)
	}
	if groups[1].Reason.Code != dead.ReasonReflection {
		t.Errorf("second group = %q, want %q", groups[1].Reason.Code, dead.ReasonReflection)
	}
}

// TestBuildDeadNeverTruncated pins customer constraint "dead first and never
// truncated": even a tiny limit keeps every dead entry, spending the cut on
// possibly_dead.
func TestBuildDeadNeverTruncated(t *testing.T) {
	findings := []dead.Finding{
		finding("A#d1", "a.rb", 1, "method", dead.VerdictDead, "", ""),
		finding("A#d2", "a.rb", 2, "method", dead.VerdictDead, "", ""),
		finding("A#d3", "a.rb", 3, "method", dead.VerdictDead, "", ""),
		finding("B#p1", "b.rb", 1, "method", dead.VerdictPossiblyDead, dead.ReasonReflection, "h"),
		finding("B#p2", "b.rb", 2, "method", dead.VerdictPossiblyDead, dead.ReasonReflection, "h"),
	}
	resp := BuildUnreferencedResponse(findings, 100, 2) // limit 2 < 3 deads

	if got := len(resp.Unreferenced.Dead); got != 3 {
		t.Errorf("dead entries = %d, want all 3 (never truncated)", got)
	}
	// Budget already exhausted by deads, so all possibly_dead are dropped but
	// reported.
	var kept, dropped int
	for _, g := range resp.Unreferenced.PossiblyDead {
		kept += len(g.Symbols)
		dropped += g.Dropped
	}
	if kept != 0 {
		t.Errorf("kept possibly_dead = %d, want 0 (budget spent on dead)", kept)
	}
	if dropped != 2 {
		t.Errorf("dropped possibly_dead = %d, want 2 (reported, not silent)", dropped)
	}
}

// TestBuildLimitPartialGroupReportsDropped: a limit that cuts a group
// mid-way keeps the budgeted symbols and records the rest as dropped.
func TestBuildLimitPartialGroupReportsDropped(t *testing.T) {
	findings := []dead.Finding{
		finding("B#p1", "b.rb", 1, "method", dead.VerdictPossiblyDead, dead.ReasonReflection, "h"),
		finding("B#p2", "b.rb", 2, "method", dead.VerdictPossiblyDead, dead.ReasonReflection, "h"),
		finding("B#p3", "b.rb", 3, "method", dead.VerdictPossiblyDead, dead.ReasonReflection, "h"),
	}
	resp := BuildUnreferencedResponse(findings, 100, 2) // no deads, budget 2

	g := resp.Unreferenced.PossiblyDead[0]
	if len(g.Symbols) != 2 {
		t.Errorf("kept = %d, want 2", len(g.Symbols))
	}
	if g.Dropped != 1 {
		t.Errorf("dropped = %d, want 1", g.Dropped)
	}
	if resp.PossiblyDeadCount != 3 {
		t.Errorf("PossiblyDeadCount = %d, want 3 (kept+dropped)", resp.PossiblyDeadCount)
	}
}

// TestBuildDeadCarriesPerSymbolVerify pins customer constraint "every verdict
// licenses an action with a check": each dead entry has a call-scoped verify
// grep excluding its own definition line.
func TestBuildDeadCarriesPerSymbolVerify(t *testing.T) {
	findings := []dead.Finding{
		finding("A#orphan", "a.rb", 12, "method", dead.VerdictDead, "", ""),
	}
	resp := BuildUnreferencedResponse(findings, 10, 0)
	v := resp.Unreferenced.Dead[0].Verify
	if !strings.Contains(v, `\.orphan`) {
		t.Errorf("verify = %q, want a call-scoped grep for the name", v)
	}
	if !strings.Contains(v, `./a.rb:12:`) {
		t.Errorf("verify = %q, want the definition line excluded", v)
	}
}

// TestBuildDeadVerifyTooCommon: a high name-occurrence count flips the dead
// verify to a manual-inspect hint.
func TestBuildDeadVerifyTooCommon(t *testing.T) {
	f := dead.Finding{
		Symbol: dead.Symbol{
			Qualified: "A#call", Name: "call", File: "a.rb", LineStart: 5,
			Kind: "method", NameOccurrences: verifyTooCommonThreshold + 1,
		},
		Verdict: dead.VerdictDead,
	}
	resp := BuildUnreferencedResponse([]dead.Finding{f}, 10, 0)
	v := resp.Unreferenced.Dead[0].Verify
	if !strings.Contains(v, "too common") {
		t.Errorf("verify = %q, want a too-common manual-inspect hint", v)
	}
}

// TestBuildGroupCarriesReasonAndVerify pins that each possibly_dead group
// carries a stable reason code, an imperative hint, and one group verify.
func TestBuildGroupCarriesReasonAndVerify(t *testing.T) {
	findings := []dead.Finding{
		finding("R#r", "r.rb", 1, "method", dead.VerdictPossiblyDead, dead.ReasonReflection, "the hint"),
	}
	resp := BuildUnreferencedResponse(findings, 10, 0)
	g := resp.Unreferenced.PossiblyDead[0]
	if g.Reason.Code != dead.ReasonReflection {
		t.Errorf("reason code = %q", g.Reason.Code)
	}
	if g.Reason.Hint != "the hint" {
		t.Errorf("reason hint = %q, want carried through", g.Reason.Hint)
	}
	if g.Verify == "" {
		t.Error("group verify must be non-empty")
	}
}

// TestBuildNeverLeaksInternalVocabulary pins the no-go: the serialized
// response must not contain "open-world" / "closed-world" / "world".
func TestBuildNeverLeaksInternalVocabulary(t *testing.T) {
	findings := []dead.Finding{
		finding("A#d", "a.rb", 1, "method", dead.VerdictDead, "", ""),
		finding("B#p", "b.rb", 1, "method", dead.VerdictPossiblyDead, dead.ReasonNoLanguageVoice, "h"),
	}
	resp := BuildUnreferencedResponse(findings, 10, 0)
	out, err := MarshalUnreferenced(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := strings.ToLower(string(out))
	for _, banned := range []string{"open-world", "closed-world", "open world", "closed world", `"world"`} {
		if strings.Contains(s, banned) {
			t.Errorf("wire output leaked internal vocabulary %q:\n%s", banned, out)
		}
	}
}

// TestBuildMissingReasonDefensive: a possibly_dead finding with a nil reason
// (contract violation) is bucketed into an "unknown" group rather than
// panicking.
func TestBuildMissingReasonDefensive(t *testing.T) {
	f := dead.Finding{
		Symbol:  dead.Symbol{Qualified: "A#x", Name: "x", File: "a.rb", LineStart: 1, Kind: "method"},
		Verdict: dead.VerdictPossiblyDead, // reason deliberately nil
	}
	resp := BuildUnreferencedResponse([]dead.Finding{f}, 10, 0)
	if len(resp.Unreferenced.PossiblyDead) != 1 {
		t.Fatalf("groups = %d, want 1", len(resp.Unreferenced.PossiblyDead))
	}
	if resp.Unreferenced.PossiblyDead[0].Reason.Code != "unknown" {
		t.Errorf("reason = %q, want defensive 'unknown'", resp.Unreferenced.PossiblyDead[0].Reason.Code)
	}
	if resp.Unreferenced.PossiblyDead[0].Verify == "" {
		t.Error("unknown group should still carry a generic verify")
	}
}

// TestMarshalUnreferencedNormalizesSlices: empty response marshals with
// `[]`, never `null`, for every slice.
func TestMarshalUnreferencedNormalizesSlices(t *testing.T) {
	out, err := MarshalUnreferenced(UnreferencedResponse{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(out, &generic); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s := string(out)
	for _, frag := range []string{`"dead":[]`, `"possibly_dead":[]`, `"next_steps":[]`} {
		if !strings.Contains(strings.ReplaceAll(s, " ", ""), frag) {
			t.Errorf("output missing normalized %q:\n%s", frag, s)
		}
	}
}

// TestMarshalUnreferencedCompactValid: compact form is valid JSON.
func TestMarshalUnreferencedCompactValid(t *testing.T) {
	findings := []dead.Finding{
		finding("A#d", "a.rb", 1, "method", dead.VerdictDead, "", ""),
	}
	out, err := MarshalUnreferencedCompact(BuildUnreferencedResponse(findings, 10, 0))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var v UnreferencedResponse
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatalf("compact output not valid JSON: %v", err)
	}
}

// TestBuildEmptyFindings: zero findings yields zero counts and empty,
// non-nil lists after marshal.
func TestBuildEmptyFindings(t *testing.T) {
	resp := BuildUnreferencedResponse(nil, 42, 0)
	if resp.DeadCount != 0 || resp.PossiblyDeadCount != 0 {
		t.Errorf("counts = %d/%d, want 0/0", resp.DeadCount, resp.PossiblyDeadCount)
	}
	if resp.TotalSymbols != 42 {
		t.Errorf("TotalSymbols = %d, want 42", resp.TotalSymbols)
	}
}

func TestBuildSymbolsOrderedWithinGroup(t *testing.T) {
	findings := []dead.Finding{
		finding("Z#z", "z.rb", 5, "method", dead.VerdictPossiblyDead, dead.ReasonReflection, "h"),
		finding("A#a", "a.rb", 9, "method", dead.VerdictPossiblyDead, dead.ReasonReflection, "h"),
		finding("A#b", "a.rb", 2, "method", dead.VerdictPossiblyDead, dead.ReasonReflection, "h"),
	}
	resp := BuildUnreferencedResponse(findings, 10, 0)
	syms := resp.Unreferenced.PossiblyDead[0].Symbols
	// Expect a.rb:2, a.rb:9, z.rb:5
	if syms[0].File != "a.rb" || syms[0].Line != 2 {
		t.Errorf("syms[0] = %s:%d, want a.rb:2", syms[0].File, syms[0].Line)
	}
	if syms[2].File != "z.rb" {
		t.Errorf("syms[2] = %s, want z.rb last", syms[2].File)
	}
}
