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
