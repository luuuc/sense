package mcpio

import (
	"testing"
	"time"
)

func TestWatchStateSetAndGet(t *testing.T) {
	ws := &WatchState{}
	ws.Set(true, time.Now())
	watching, since := ws.Get()
	if !watching {
		t.Error("expected watching=true after Set")
	}
	if since.IsZero() {
		t.Error("expected non-zero since after Set")
	}
}

func TestWatchStateSetFalse(t *testing.T) {
	ws := &WatchState{}
	ws.Set(false, time.Time{})
	watching, since := ws.Get()
	if watching {
		t.Error("expected watching=false after Set(false)")
	}
	if !since.IsZero() {
		t.Error("expected zero since when watching=false")
	}
}

func TestWatchStateSetIndexedSnapshot(t *testing.T) {
	ws := &WatchState{}
	start := time.Now().Add(-time.Minute)
	ws.Set(true, start)
	idx := time.Now()
	ws.SetIndexed(idx, 7)

	on, since, lastIndexed, pending := ws.Snapshot()
	if !on {
		t.Error("expected watching")
	}
	if !since.Equal(start) {
		t.Errorf("since = %v, want %v", since, start)
	}
	if !lastIndexed.Equal(idx) {
		t.Errorf("lastIndexed = %v, want %v", lastIndexed, idx)
	}
	if pending != 7 {
		t.Errorf("pending = %d, want 7", pending)
	}
}

func TestWatchStateSetGet(t *testing.T) {
	ws := &WatchState{}

	on, since := ws.Get()
	if on {
		t.Error("initial state should be off")
	}
	if !since.IsZero() {
		t.Error("initial since should be zero")
	}

	now := time.Now().UTC()
	ws.Set(true, now)

	on, since = ws.Get()
	if !on {
		t.Error("expected watching=true after Set")
	}
	if !since.Equal(now) {
		t.Errorf("since = %v, want %v", since, now)
	}

	ws.Set(false, time.Time{})
	on, _ = ws.Get()
	if on {
		t.Error("expected watching=false after Set(false)")
	}
}
