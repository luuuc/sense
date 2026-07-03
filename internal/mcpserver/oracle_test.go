package mcpserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	mcp "github.com/mark3labs/mcp-go/mcp"
)

// The response oracle is the deterministic net under the mcpserver split
// (pitch 27-05). It drives a fixed, ordered set of calls — every tool plus the
// not-found, disambiguation, and error responses — through the deterministic
// fixtures, normalizes the volatile bits (freshness timestamps, on-disk index
// size), and hashes WHAT each handler returned into a single fingerprint. A
// pure file move or a behavior-preserving decomposition must leave the digest
// unchanged; a change that alters any response fails here loudly. The existing
// typed-shape assertions pin the per-field contract; this is the one repeatable
// before/after check that the whole response set is identical.

// volatileKeys are response fields whose value depends on wall-clock time or
// the on-disk index file rather than the indexed content. They are redacted
// before hashing so the digest depends only on derived response structure.
var volatileKeys = map[string]bool{
	"last_scan":                 true,
	"index_age_seconds":         true,
	"last_update":               true,
	"index_update_age_seconds":  true,
	"max_file_mtime_since_scan": true,
	"watch_since":               true,
	"size_bytes":                true,
}

func redactVolatile(v any) {
	switch x := v.(type) {
	case map[string]any:
		for k := range x {
			if volatileKeys[k] {
				x[k] = "<redacted>"
				continue
			}
			redactVolatile(x[k])
		}
	case []any:
		for i := range x {
			redactVolatile(x[i])
		}
	}
}

// normalizeResponse canonicalizes one handler response. JSON responses are
// re-marshalled with redacted volatile fields (Go sorts map keys, so the form
// is stable). Plain-string error results (e.g. "missing required parameter")
// are not JSON and are hashed verbatim.
func normalizeResponse(t *testing.T, raw string) string {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return raw
	}
	redactVolatile(v)
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	return string(out)
}

// labeledCall pairs a stable label with a handler's response text.
type labeledCall struct {
	label string
	text  string
}

func callText(t *testing.T, result *mcp.CallToolResult, err error) string {
	t.Helper()
	if err != nil {
		t.Fatalf("handler returned a Go error: %v", err)
	}
	return resultText(t, result)
}

// collectOracleCalls drives the fixed call set and returns the labeled
// responses in order. The order is part of the oracle — it is not sorted.
func collectOracleCalls(t *testing.T) []labeledCall {
	t.Helper()
	ctx := context.Background()

	ts := setupTestServer(t)
	h := ts.handlers

	rf := setupResolveFixture(t)

	calls := []labeledCall{}
	add := func(label string, result *mcp.CallToolResult, err error) {
		calls = append(calls, labeledCall{label: label, text: callText(t, result, err)})
	}

	// Every tool, happy path.
	r, err := h.handleSearch(ctx, toolReq(map[string]any{"query": "auth"}))
	add("search/auth", r, err)

	r, err = h.handleGraph(ctx, toolReq(map[string]any{"symbol": "auth.Verify", "direction": "both"}))
	add("graph/verify/both", r, err)

	r, err = h.handleGraph(ctx, toolReq(map[string]any{"symbol": "auth.Verify", "direction": "callers"}))
	add("graph/verify/callers", r, err)

	r, err = h.handleGraph(ctx, toolReq(map[string]any{"symbol": "handler.HandleRequest", "direction": "callees"}))
	add("graph/handlerequest/callees", r, err)

	r, err = h.handleGraph(ctx, toolReq(map[string]any{"dead_code": true}))
	add("graph/dead_code", r, err)

	r, err = h.handleBlast(ctx, toolReq(map[string]any{"symbol": "auth.Verify"}))
	add("blast/verify", r, err)

	r, err = h.handleConventions(ctx, toolReq(map[string]any{}))
	add("conventions", r, err)

	r, err = h.handleStatus(ctx, mcp.CallToolRequest{})
	add("status", r, err)

	// Not-found and error responses.
	r, err = h.handleGraph(ctx, toolReq(map[string]any{"symbol": "DoesNotExist"}))
	add("graph/not_found", r, err)

	r, err = h.handleGraph(ctx, toolReq(map[string]any{"symbol": ""}))
	add("graph/missing_param", r, err)

	r, err = h.handleBlast(ctx, toolReq(map[string]any{"symbol": "auth.Verify", "diff": "HEAD~1"}))
	add("blast/both_args", r, err)

	r, err = h.handleBlast(ctx, toolReq(map[string]any{}))
	add("blast/no_args", r, err)

	// Disambiguation needs colliding base names — use the resolve fixture.
	r, err = rf.h.handleGraph(ctx, toolReq(map[string]any{"symbol": "Handle"}))
	add("graph/disambiguation", r, err)

	return calls
}

func oracleDigest(t *testing.T, calls []labeledCall) (string, []string) {
	t.Helper()
	lines := make([]string, len(calls))
	for i, c := range calls {
		lines[i] = c.label + "\t" + normalizeResponse(t, c.text)
	}
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:]), lines
}

// oracleGolden is the content fingerprint of the fixed call set. It is pinned,
// not computed at runtime: a behavior-preserving split must leave it unchanged.
// When a response changes on purpose, run the test with -v, copy the logged
// digest here, and update it in the same commit.
//
// Last bumped: conventions now exclude test-file symbols from the domain-structure
// detectors, so the `conventions` response's structure line moved from counting
// the fixture's test function to domain-only (internal/auth/ "3 of 6"). No other
// response changed.
// Added the `completeness` verdict to blast/graph responses and per-caller
// `relation` to blast callers (terminal payloads) — digest moved.
// blast direct_callers are now enumerated area-stratified (breadth-first
// across subsystems) and emitted area-clustered, so the fixture's two
// callers reorder by area name (internal/auth before internal/handler)
// instead of by symbol ID — digest moved.
// Per-session blast dedup: the three graph calls on auth.Verify earlier in
// this sequence mark its call-edge targets seen, so the blast on auth.Verify
// now collapses both already-returned direct callers into seen_elsewhere
// (direct_callers: [], seen_elsewhere.count: 2). Magnitude (total_affected,
// direct_callers_by_area, affected_files) and the "complete" verdict are
// unchanged — only the duplicate enumeration is gone — so the digest moved.
// All direct callers of auth.Verify are collapsed (count == directTier1), so
// the seen_elsewhere note now uses the "all N … see that response" phrasing
// instead of "only new callers are listed" — digest moved.
// Conventions category ordering was re-ranked by how much a category reveals the
// project's own architecture (inheritance, framework, design-pattern, composition
// lead; naming/structure/testing trail), so the fixture's key_types line now
// precedes its structure line — digest moved.
// sense_graph now splits inbound composition into its own `composed_by` bucket
// (reverse-composition, distinct from outbound `composes`), so every graph
// response gained an empty `composed_by` field and callees-direction responses
// drop it — digest moved. No edge content changed.
// sense_graph now splits inbound inheritance into its own `inherited_by` bucket
// (subtypes, distinct from outbound `inherits` supertypes), mirroring the
// composed_by split, so every graph response gained an empty `inherited_by`
// field; callers-direction responses drop `inherits` and callees drop
// `inherited_by` — digest moved. No edge content changed.
const oracleGolden = "e48785ad58f92c14e3885935ce38124825dc0850a6d1879c02ca698400f421c0"

func TestMCPServerResponseOracle(t *testing.T) {
	got, content := oracleDigest(t, collectOracleCalls(t))
	t.Logf("oracle digest: %s", got)
	if oracleGolden == "PENDING_CAPTURE" {
		t.Skipf("oracle golden not yet pinned; capture digest: %s", got)
	}
	if got != oracleGolden {
		t.Errorf("response oracle digest changed:\n got  %s\n want %s\nIf this change is intentional, update oracleGolden. Normalized responses:\n%s",
			got, oracleGolden, strings.Join(content, "\n"))
	}
}

// TestMCPServerResponseOracleStable proves the digest is the net it claims to
// be: two independent runs over the same fixtures produce the same fingerprint.
// Guards against nondeterminism that would make the before/after check useless.
func TestMCPServerResponseOracleStable(t *testing.T) {
	first, _ := oracleDigest(t, collectOracleCalls(t))
	again, _ := oracleDigest(t, collectOracleCalls(t))
	if first != again {
		t.Errorf("oracle digest not stable across runs:\n first %s\n again %s", first, again)
	}
}
