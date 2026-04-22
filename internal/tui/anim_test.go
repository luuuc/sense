package tui

import (
	"testing"
	"time"
)

func TestAnimation_EaseOutCubic(t *testing.T) {
	if got := easeOutCubic(0); got != 0 {
		t.Errorf("easeOutCubic(0) = %f, want 0", got)
	}
	if got := easeOutCubic(1); got != 1 {
		t.Errorf("easeOutCubic(1) = %f, want 1", got)
	}
	mid := easeOutCubic(0.5)
	if mid < 0.5 || mid > 1.0 {
		t.Errorf("easeOutCubic(0.5) = %f, expected > 0.5 (decelerating curve)", mid)
	}
}

func TestAnimation_NewZeroNodes(t *testing.T) {
	a := newAnimation(0)
	if a.active {
		t.Error("animation with 0 nodes should not be active")
	}
}

func TestAnimation_RevealOrder(t *testing.T) {
	a := newAnimation(100)
	a.active = true
	a.startTime = time.Now().Add(-500 * time.Millisecond) // 25% through

	count, cmd := a.update()
	if count < 50 {
		t.Errorf("at 25%% elapsed with ease-out, expected >= 50 nodes visible, got %d", count)
	}
	if count >= 100 {
		t.Errorf("at 25%% elapsed, should not have all nodes visible, got %d", count)
	}
	if cmd == nil {
		t.Error("animation should still be ticking")
	}
}

func TestAnimation_Completion(t *testing.T) {
	a := newAnimation(50)
	a.active = true
	a.startTime = time.Now().Add(-3 * time.Second) // past duration

	count, cmd := a.update()
	if count != 50 {
		t.Errorf("completed animation should show all nodes, got %d", count)
	}
	if cmd != nil {
		t.Error("completed animation should return nil cmd")
	}
	if a.active {
		t.Error("animation should be inactive after completion")
	}
}

func TestAnimation_StartReturnsCmd(t *testing.T) {
	a := newAnimation(10)
	cmd := a.start()
	if cmd == nil {
		t.Error("start() should return a tick command")
	}
}

func TestAnimation_InactiveStartReturnsNil(t *testing.T) {
	a := newAnimation(0)
	cmd := a.start()
	if cmd != nil {
		t.Error("inactive animation start() should return nil")
	}
}

func TestAnimation_VisibleCountIntegration(t *testing.T) {
	layout := testLayout()
	m := newModel(graphStats{}, layout, nil, nil)

	if !m.anim.active {
		t.Error("animation should be active for non-empty layout")
	}

	// Simulate completion
	m.anim.startTime = time.Now().Add(-3 * time.Second)
	count, _ := m.anim.update()
	m.renderer.VisibleCount = count

	if m.renderer.VisibleCount != len(layout.Nodes) {
		t.Errorf("after animation, VisibleCount should be %d, got %d",
			len(layout.Nodes), m.renderer.VisibleCount)
	}
}
