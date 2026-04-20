package mcpio

import (
	"sync"
	"time"
)

// WatchState is a thread-safe container for watch mode status. It is
// shared between the watch loop (writer) and the MCP server (reader).
type WatchState struct {
	mu    sync.RWMutex
	on    bool
	since time.Time
}

// Set updates the watch state.
func (ws *WatchState) Set(watching bool, since time.Time) {
	ws.mu.Lock()
	ws.on = watching
	ws.since = since
	ws.mu.Unlock()
}

// Get returns the current watch state.
func (ws *WatchState) Get() (watching bool, since time.Time) {
	ws.mu.RLock()
	defer ws.mu.RUnlock()
	return ws.on, ws.since
}
