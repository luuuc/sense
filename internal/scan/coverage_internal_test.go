package scan

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/model"
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
		Walk:      500 * time.Millisecond,
		Embed:     300 * time.Millisecond,
		BuildHNSW: 200 * time.Millisecond,
	}
	printPhaseBreakdown(&buf, time.Second, phases)
	out := buf.String()

	if !strings.Contains(out, "embed") {
		t.Errorf("output should contain embed phase: %q", out)
	}
	if !strings.Contains(out, "hnsw") {
		t.Errorf("output should contain hnsw phase: %q", out)
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

func TestProgressRender(t *testing.T) {
	var buf bytes.Buffer
	p := &progress{
		out:     &buf,
		enabled: true,
		done:    make(chan struct{}),
	}
	p.phase.Store("Scanning...")
	p.total.Store(100)
	p.current.Store(42)
	p.warnings.Store(3)

	p.render()
	out := buf.String()

	if !strings.Contains(out, "Scanning...") {
		t.Errorf("render output missing phase name: %q", out)
	}
	if !strings.Contains(out, "42/100") {
		t.Errorf("render output missing counter: %q", out)
	}
	if !strings.Contains(out, "3 warnings") {
		t.Errorf("render output missing warnings: %q", out)
	}
}

func TestProgressRenderEmptyPhase(t *testing.T) {
	var buf bytes.Buffer
	p := &progress{
		out:     &buf,
		enabled: true,
		done:    make(chan struct{}),
	}
	p.phase.Store("")
	p.render()
	if buf.Len() != 0 {
		t.Errorf("empty phase should produce no output, got %q", buf.String())
	}
}

func TestProgressRenderNoTotal(t *testing.T) {
	var buf bytes.Buffer
	p := &progress{
		out:     &buf,
		enabled: true,
		done:    make(chan struct{}),
	}
	p.phase.Store("Resolving...")
	p.total.Store(0)
	p.current.Store(0)

	p.render()
	out := buf.String()
	if !strings.Contains(out, "Resolving...") {
		t.Errorf("render output missing phase: %q", out)
	}
	if strings.Contains(out, "/") {
		t.Errorf("should not show counter when total is 0: %q", out)
	}
}

func TestProgressStartStop(t *testing.T) {
	var buf bytes.Buffer
	p := &progress{
		out:     &buf,
		enabled: true,
		done:    make(chan struct{}),
	}
	p.phase.Store("Testing...")
	p.total.Store(10)
	p.start()
	p.stop()
	p.stop() // double stop should be safe (sync.Once)

	select {
	case <-p.done:
	default:
		t.Error("done channel should be closed after stop")
	}
}

func TestProgressClear(t *testing.T) {
	var buf bytes.Buffer
	p := &progress{
		out:     &buf,
		enabled: true,
		done:    make(chan struct{}),
	}
	p.clear()
	if !strings.HasPrefix(buf.String(), "\r") {
		t.Error("clear should start with \\r")
	}
}

func TestRepresentativeTestSymbol(t *testing.T) {
	t.Run("empty slice", func(t *testing.T) {
		_, ok := representativeTestSymbol(nil)
		if ok {
			t.Error("expected false for nil slice")
		}
	})

	t.Run("picks earliest line", func(t *testing.T) {
		symbols := []model.Symbol{
			{ID: 10, Name: "TestB", LineStart: 20},
			{ID: 5, Name: "TestA", LineStart: 5},
			{ID: 8, Name: "TestC", LineStart: 15},
		}
		id, ok := representativeTestSymbol(symbols)
		if !ok {
			t.Fatal("expected true")
		}
		if id != 5 {
			t.Errorf("got ID %d, want 5 (TestA at line 5)", id)
		}
	})

	t.Run("ties broken by ID", func(t *testing.T) {
		symbols := []model.Symbol{
			{ID: 10, Name: "TestB", LineStart: 1},
			{ID: 3, Name: "TestA", LineStart: 1},
		}
		id, ok := representativeTestSymbol(symbols)
		if !ok {
			t.Fatal("expected true")
		}
		if id != 3 {
			t.Errorf("got ID %d, want 3 (lower ID tie-break)", id)
		}
	})
}

func TestAddWarning(t *testing.T) {
	wc := newWarningCollector()
	p := &progress{
		out:     &bytes.Buffer{},
		enabled: false,
		done:    make(chan struct{}),
	}
	p.phase.Store("")

	h := &harness{
		collector: wc,
		progress:  p,
	}

	h.addWarning(warnParseFailed, "test.go (%s)", "broken syntax")

	if wc.count() != 1 {
		t.Errorf("warning count = %d, want 1", wc.count())
	}
	if p.warnings.Load() != 1 {
		t.Errorf("progress warnings = %d, want 1", p.warnings.Load())
	}
}

func TestMethodSetSatisfies(t *testing.T) {
	methods := map[string]bool{"Read": true, "Write": true, "Close": true}

	if !methodSetSatisfies(methods, []string{"Read", "Write"}) {
		t.Error("should satisfy subset")
	}
	if !methodSetSatisfies(methods, []string{"Read", "Write", "Close"}) {
		t.Error("should satisfy exact set")
	}
	if methodSetSatisfies(methods, []string{"Read", "Flush"}) {
		t.Error("should not satisfy with missing method")
	}
	if !methodSetSatisfies(methods, nil) {
		t.Error("nil required should satisfy")
	}
}

func TestPromoteEmbeddedMethods(t *testing.T) {
	outer := &structInfo{methods: map[string]bool{"Own": true}}
	inner := &structInfo{methods: map[string]bool{"Read": true, "Write": true}}

	structs := map[int64]*structInfo{
		1: outer,
		2: inner,
	}
	embeddings := map[int64][]int64{
		1: {2},
	}

	promoteEmbeddedMethods(outer, 1, embeddings, structs, 3)

	if !outer.methods["Read"] {
		t.Error("expected Read promoted from embedded struct")
	}
	if !outer.methods["Write"] {
		t.Error("expected Write promoted from embedded struct")
	}
	if !outer.methods["Own"] {
		t.Error("expected Own to remain")
	}
}

func TestPromoteEmbeddedMethodsDepthLimit(t *testing.T) {
	a := &structInfo{methods: map[string]bool{}}
	b := &structInfo{methods: map[string]bool{}}
	c := &structInfo{methods: map[string]bool{"Deep": true}}

	structs := map[int64]*structInfo{1: a, 2: b, 3: c}
	embeddings := map[int64][]int64{1: {2}, 2: {3}}

	promoteEmbeddedMethods(a, 1, embeddings, structs, 1)

	// Depth=1 means we only go one hop. b's methods get promoted,
	// but c's methods should not (they need depth=2).
	if a.methods["Deep"] {
		t.Error("expected depth limit to prevent promoting from 2 hops away")
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
