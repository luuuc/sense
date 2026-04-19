package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/luuuc/sense/internal/mcpio"
)

// RenderGraphHuman writes the opinionated single-column text
// rendering of a GraphResponse — the shape shown in the pitch's
// "Human output" example:
//
//	CheckoutService  (class)
//	  app/services/checkout_service.rb:12-85
//	  inherits  ApplicationService
//	  calls     PaymentGateway#charge, Order#finalize, Beacon.track (0.9)
//	  callers   OrdersController#create, CheckoutJob#perform
//	  tests     test/services/checkout_service_test.rb (0.8)
//
// Group lines are suppressed when the corresponding edge slice is
// empty so a symbol with only callers doesn't print "calls" and
// "inherits" as ghost groups. Confidence annotations are inline,
// shown only when <1.0 (the pitch's "Confidence shown when <1.0"
// rule).
func RenderGraphHuman(w io.Writer, resp mcpio.GraphResponse) {
	_, _ = fmt.Fprintf(w, "%s  (%s)\n", resp.Symbol.Name, resp.Symbol.Kind)
	_, _ = fmt.Fprintf(w, "  %s:%d-%d\n", resp.Symbol.File, resp.Symbol.LineStart, resp.Symbol.LineEnd)

	// Group order matches the pitch example: inherits → calls →
	// callers → tests. Labels are padded to a shared width so the
	// edge lists line up on a common left edge.
	const label = "  %-9s %s\n"

	if s := renderInherits(resp.Edges.Inherits); s != "" {
		_, _ = fmt.Fprintf(w, label, "inherits", s)
	}
	if s := renderCalls(resp.Edges.Calls); s != "" {
		_, _ = fmt.Fprintf(w, label, "calls", s)
	}
	if s := renderCalls(resp.Edges.CalledBy); s != "" {
		_, _ = fmt.Fprintf(w, label, "callers", s)
	}
	if s := renderTests(resp.Edges.Tests); s != "" {
		_, _ = fmt.Fprintf(w, label, "tests", s)
	}
}

// renderCalls joins call-edge entries as "sym, sym (0.9), sym". A
// confidence annotation appears only when the edge's confidence is
// strictly less than 1.0 — the pitch's visual signal that the edge
// is heuristic rather than statically proven.
func renderCalls(edges []mcpio.CallEdgeRef) string {
	if len(edges) == 0 {
		return ""
	}
	parts := make([]string, 0, len(edges))
	for _, e := range edges {
		parts = append(parts, withConfidence(e.Symbol, e.Confidence))
	}
	return strings.Join(parts, ", ")
}

// renderInherits joins inherits entries; inheritance has no
// confidence on the wire (the schema omits it), so entries are
// bare symbol names.
func renderInherits(edges []mcpio.InheritEdgeRef) string {
	if len(edges) == 0 {
		return ""
	}
	parts := make([]string, 0, len(edges))
	for _, e := range edges {
		parts = append(parts, e.Symbol)
	}
	return strings.Join(parts, ", ")
}

// renderTests joins test-file entries with confidence annotation
// rules matching renderCalls: show confidence only when <1.0.
func renderTests(edges []mcpio.TestEdgeRef) string {
	if len(edges) == 0 {
		return ""
	}
	parts := make([]string, 0, len(edges))
	for _, e := range edges {
		parts = append(parts, withConfidence(e.File, e.Confidence))
	}
	return strings.Join(parts, ", ")
}

// withConfidence appends " (c)" when c < 1.0, else returns the
// label unchanged. Confidence is formatted with the %g verb so
// "0.9" stays "0.9" rather than the wire-canonical "0.9"; on the
// human path the extra decimal is noise.
func withConfidence(label string, c mcpio.Confidence) string {
	if float64(c) >= 1.0 {
		return label
	}
	return fmt.Sprintf("%s (%g)", label, float64(c))
}

// RenderBlastHuman writes the single-column blast rendering. Risk
// factors inline into the subject line because they are always
// short phrases ("hub node", "11 direct callers"); a dedicated
// bullet list would repeat the caller count already shown in the
// "Direct callers (N):" section header. Sections collapse when
// empty so a symbol with no callers does not print empty headers.
func RenderBlastHuman(w io.Writer, resp mcpio.BlastResponse) {
	if len(resp.RiskFactors) > 0 {
		_, _ = fmt.Fprintf(w, "%s  risk: %s  (%s)\n", resp.Symbol, resp.Risk,
			strings.Join(resp.RiskFactors, ", "))
	} else {
		_, _ = fmt.Fprintf(w, "%s  risk: %s\n", resp.Symbol, resp.Risk)
	}
	if len(resp.DirectCallers) > 0 {
		_, _ = fmt.Fprintf(w, "\nDirect callers (%d):\n", len(resp.DirectCallers))
		for _, c := range resp.DirectCallers {
			_, _ = fmt.Fprintf(w, "  %s  %s\n", c.Symbol, c.File)
		}
	}
	if len(resp.IndirectCallers) > 0 {
		_, _ = fmt.Fprintf(w, "\nIndirect callers (%d):\n", len(resp.IndirectCallers))
		for _, c := range resp.IndirectCallers {
			_, _ = fmt.Fprintf(w, "  %s  via %s (%d hops)\n", c.Symbol, c.Via, c.Hops)
		}
	}
	if len(resp.AffectedTests) > 0 {
		_, _ = fmt.Fprintf(w, "\nAffected tests (%d):\n", len(resp.AffectedTests))
		for _, t := range resp.AffectedTests {
			_, _ = fmt.Fprintf(w, "  %s\n", t)
		}
	}
	_, _ = fmt.Fprintf(w, "\nTotal affected: %d\n", resp.TotalAffected)
}
