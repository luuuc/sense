package tui

import (
	"testing"
	"time"
)

func TestPulseState_Render(t *testing.T) {
	p := newPulseState(true)
	got := p.render(p.startTime)
	if !containsText(got, pulseChar) {
		t.Errorf("pulse should render %q, got %q", pulseChar, got)
	}
}

func TestPulseState_RenderLight(t *testing.T) {
	p := newPulseState(false)
	got := p.render(p.startTime)
	if !containsText(got, pulseChar) {
		t.Errorf("pulse should render %q in light mode, got %q", pulseChar, got)
	}
}

func TestPulseState_BreatheBrightness_Cycles(t *testing.T) {
	p := newPulseState(true)
	base := p.startTime

	b0 := p.breatheBrightness(base)
	b750 := p.breatheBrightness(base.Add(750 * time.Millisecond))
	b1500 := p.breatheBrightness(base.Add(1500 * time.Millisecond))
	b2250 := p.breatheBrightness(base.Add(2250 * time.Millisecond))

	if b750 <= b0 {
		t.Error("brightness should increase in first quarter")
	}
	if b1500 >= b750 {
		t.Error("brightness should decrease after peak")
	}
	if b2250 >= b1500 {
		t.Error("brightness should continue decreasing toward trough")
	}
}

func TestPulseState_BreatheBrightness_Range(t *testing.T) {
	p := newPulseState(true)
	base := p.startTime

	for i := 0; i < 60; i++ {
		ts := base.Add(time.Duration(i) * 100 * time.Millisecond)
		b := p.breatheBrightness(ts)
		if b < 0.3 || b > 1.0 {
			t.Errorf("brightness at %v = %f, want [0.3, 1.0]", ts.Sub(base), b)
		}
	}
}

func TestPulseState_Event_FlashAtMaxBrightness(t *testing.T) {
	p := newPulseState(true)
	p.startTime = time.Now().Add(-10 * time.Second)
	p.event()

	duringFlash := p.eventTime.Add(100 * time.Millisecond)
	rendered := p.render(duringFlash)
	vividHex := interpolateColor(1.0, true)

	if !containsText(rendered, pulseChar) {
		t.Error("should render pulse char during flash")
	}
	if p.cachedHex != vividHex {
		t.Errorf("during flash should be vivid: got %s, want %s", p.cachedHex, vividHex)
	}
}

func TestPulseState_Event_DecaysSmooth(t *testing.T) {
	p := newPulseState(true)
	p.startTime = time.Now().Add(-10 * time.Second)
	p.event()

	flashTime := p.eventTime.Add(100 * time.Millisecond)
	midDecayTime := p.eventTime.Add(eventFlash + eventDecay/2)

	p.render(flashTime)
	flashHex := p.cachedHex

	p.render(midDecayTime)
	midHex := p.cachedHex

	breatheHex := interpolateColor(p.breatheBrightness(midDecayTime), true)

	if midHex == flashHex {
		t.Error("mid-decay should not equal flash brightness (should be decaying)")
	}
	if midHex == breatheHex {
		t.Error("mid-decay should not equal pure breathe (still influenced by event)")
	}
}

func TestPulseState_Event_ExpiresBackToBreathe(t *testing.T) {
	p := newPulseState(true)
	p.startTime = time.Now().Add(-10 * time.Second)
	p.event()

	wellAfter := p.eventTime.Add(eventFlash + eventDecay + time.Second)

	p.render(wellAfter)
	actualHex := p.cachedHex

	expectedHex := interpolateColor(p.breatheBrightness(wellAfter), true)
	if actualHex != expectedHex {
		t.Errorf("after event expires, should return to breathe curve: got %s, want %s", actualHex, expectedHex)
	}
}

func TestPulseState_StyleCache(t *testing.T) {
	p := newPulseState(true)
	now := p.startTime

	p.render(now)
	hex1 := p.cachedHex

	p.render(now)
	if p.cachedHex != hex1 {
		t.Error("same time should produce same cached hex")
	}

	later := now.Add(breathePeriod / 4)
	p.render(later)
	if p.cachedHex == hex1 {
		t.Error("different brightness should update cached hex")
	}
}

func TestInterpolateColor_Bounds(t *testing.T) {
	c0 := interpolateColor(0, true)
	c1 := interpolateColor(1, true)

	if c0 == c1 {
		t.Error("brightness 0 and 1 should produce different colors")
	}
	if interpolateColor(-0.5, true) != c0 {
		t.Error("negative brightness should clamp to 0")
	}
	if interpolateColor(1.5, true) != c1 {
		t.Error("brightness >1 should clamp to 1")
	}
}

func TestInterpolateColor_LightVsDark(t *testing.T) {
	dark := interpolateColor(0.5, true)
	light := interpolateColor(0.5, false)
	if dark == light {
		t.Error("dark and light modes should produce different colors")
	}
}

func TestPulseTick_ReturnsCmd(t *testing.T) {
	cmd := pulseTick()
	if cmd == nil {
		t.Error("pulseTick should return a non-nil cmd")
	}
}
