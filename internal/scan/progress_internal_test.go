package scan

import (
	"bytes"
	"strings"
	"testing"
)

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
