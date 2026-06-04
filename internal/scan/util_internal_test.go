package scan

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestPrintPhaseBreakdown(t *testing.T) {
	var buf bytes.Buffer
	phases := PhaseTiming{
		Walk:              300 * time.Millisecond,
		RemoveStale:       50 * time.Millisecond,
		ResolveEdges:      200 * time.Millisecond,
		SatisfyInterfaces: 100 * time.Millisecond,
		AssociateTests:    100 * time.Millisecond,
		NamingConventions: 50 * time.Millisecond,
		Temporal:          200 * time.Millisecond,
	}
	printPhaseBreakdown(&buf, time.Second, phases)
	out := buf.String()

	if !strings.Contains(out, "walk") {
		t.Errorf("output missing walk phase: %q", out)
	}
	if !strings.Contains(out, "30%") {
		t.Errorf("output missing 30%% for walk: %q", out)
	}
	if strings.Contains(out, "embed") {
		t.Errorf("output should not contain embed when Embed is 0: %q", out)
	}
}

func TestPrintPhaseBreakdownWithEmbeddings(t *testing.T) {
	var buf bytes.Buffer
	phases := PhaseTiming{
		Walk:  500 * time.Millisecond,
		Embed: 300 * time.Millisecond,
	}
	printPhaseBreakdown(&buf, time.Second, phases)
	out := buf.String()

	if !strings.Contains(out, "embed") {
		t.Errorf("output should contain embed phase: %q", out)
	}
}

func TestPrintPhaseBreakdownZeroTotal(t *testing.T) {
	var buf bytes.Buffer
	printPhaseBreakdown(&buf, 0, PhaseTiming{Walk: time.Millisecond})
	out := buf.String()
	if !strings.Contains(out, "0%") {
		t.Errorf("zero total should produce 0%% percentages: %q", out)
	}
}

func TestSnippetForLineEdgeCases(t *testing.T) {
	source := []byte("line one\nline two\nline three\n")

	if got := snippetForLine(source, 0); got != "" {
		t.Errorf("line 0 should be empty, got %q", got)
	}
	if got := snippetForLine(source, -1); got != "" {
		t.Errorf("line -1 should be empty, got %q", got)
	}
	if got := snippetForLine(source, 100); got != "" {
		t.Errorf("line 100 should be empty, got %q", got)
	}
	if got := snippetForLine(source, 2); got != "line two" {
		t.Errorf("line 2 = %q, want %q", got, "line two")
	}
}
