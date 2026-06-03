package metrics

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// Tracker accumulates per-query savings estimates into session (in-memory)
// and lifetime (SQLite-backed) counters. Session counters reset on process
// start. Lifetime counters flush to the sense_metrics table periodically
// and on graceful shutdown.
type Tracker struct {
	db            *sql.DB
	flushInterval time.Duration

	session struct {
		queries           atomic.Int64
		fileReadsAvoided  atomic.Int64
		tokensSaved       atomic.Int64
		textFallbackFired atomic.Int64
	}

	topQuery struct {
		mu          sync.Mutex
		tool        string
		args        string
		tokensSaved int
	}

	lifetime struct {
		mu                sync.Mutex
		queries           int64
		fileReadsAvoided  int64
		tokensSaved       int64
		textFallbackFired int64
	}

	// flushMu serializes the DB transaction in flush so that the write-through
	// flush triggered by each Record never contends with another Record's
	// flush (or the background ticker) on the single SQLite writer.
	flushMu sync.Mutex

	stop chan struct{}
	done chan struct{}
}

// NewTracker creates a tracker that flushes lifetime counters to db every 30s.
// Call Close to flush final counters and stop the background goroutine.
func NewTracker(db *sql.DB) *Tracker {
	return NewTrackerWithInterval(db, 30*time.Second)
}

// NewTrackerWithInterval creates a tracker with a custom flush interval.
func NewTrackerWithInterval(db *sql.DB, interval time.Duration) *Tracker {
	t := &Tracker{
		db:            db,
		flushInterval: interval,
		stop:          make(chan struct{}),
		done:          make(chan struct{}),
	}
	go t.flushLoop()
	return t
}

// Record adds a query's estimates to both session and lifetime counters, then
// flushes the lifetime delta to SQLite write-through. Persisting on every query
// (rather than only on the 30s ticker or a graceful Close) keeps the durable
// lifetime totals accurate even when the MCP server process is restarted on
// reconnect/compaction or killed without a clean shutdown — the churn that also
// resets the in-memory session counters.
func (t *Tracker) Record(tool, args string, fileReadsAvoided, tokensSaved int, textFallback bool) {
	t.session.queries.Add(1)
	t.session.fileReadsAvoided.Add(int64(fileReadsAvoided))
	t.session.tokensSaved.Add(int64(tokensSaved))
	if textFallback {
		t.session.textFallbackFired.Add(1)
	}

	t.lifetime.mu.Lock()
	t.lifetime.queries++
	t.lifetime.fileReadsAvoided += int64(fileReadsAvoided)
	t.lifetime.tokensSaved += int64(tokensSaved)
	if textFallback {
		t.lifetime.textFallbackFired++
	}
	t.lifetime.mu.Unlock()

	t.topQuery.mu.Lock()
	if tokensSaved > t.topQuery.tokensSaved {
		t.topQuery.tool = tool
		t.topQuery.args = args
		t.topQuery.tokensSaved = tokensSaved
	}
	t.topQuery.mu.Unlock()

	// Write-through. A transient failure (e.g. the DB briefly locked) leaves
	// the delta in the in-memory buffer; the background ticker and Close retry
	// it, so no increment is dropped.
	t.flush()
}

// Session returns current session counters.
func (t *Tracker) Session() SessionCounters {
	return SessionCounters{
		Queries:           int(t.session.queries.Load()),
		FileReadsAvoided:  int(t.session.fileReadsAvoided.Load()),
		TokensSaved:       int(t.session.tokensSaved.Load()),
		TextFallbackFired: int(t.session.textFallbackFired.Load()),
	}
}

// TopQuery returns the highest-saving query this session.
func (t *Tracker) TopQuery() *TopQueryInfo {
	t.topQuery.mu.Lock()
	defer t.topQuery.mu.Unlock()
	if t.topQuery.tokensSaved == 0 {
		return nil
	}
	return &TopQueryInfo{
		Tool:        t.topQuery.tool,
		Args:        t.topQuery.args,
		TokensSaved: t.topQuery.tokensSaved,
	}
}

// Lifetime returns lifetime counters (flushed + pending).
func (t *Tracker) Lifetime(ctx context.Context) LifetimeCounters {
	persisted := t.loadPersisted(ctx)

	t.lifetime.mu.Lock()
	pending := LifetimeCounters{
		Queries:           persisted.Queries + int(t.lifetime.queries),
		FileReadsAvoided:  persisted.FileReadsAvoided + int(t.lifetime.fileReadsAvoided),
		TokensSaved:       persisted.TokensSaved + int(t.lifetime.tokensSaved),
		TextFallbackFired: persisted.TextFallbackFired + int(t.lifetime.textFallbackFired),
	}
	t.lifetime.mu.Unlock()
	return pending
}

// Close flushes pending lifetime counters and stops the background goroutine.
func (t *Tracker) Close() {
	close(t.stop)
	<-t.done
	t.flush()
}

func (t *Tracker) flushLoop() {
	defer close(t.done)
	ticker := time.NewTicker(t.flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			t.flush()
		case <-t.stop:
			return
		}
	}
}

func (t *Tracker) flush() {
	t.flushMu.Lock()
	defer t.flushMu.Unlock()

	t.lifetime.mu.Lock()
	q := t.lifetime.queries
	f := t.lifetime.fileReadsAvoided
	ts := t.lifetime.tokensSaved
	tf := t.lifetime.textFallbackFired
	t.lifetime.queries = 0
	t.lifetime.fileReadsAvoided = 0
	t.lifetime.tokensSaved = 0
	t.lifetime.textFallbackFired = 0
	t.lifetime.mu.Unlock()

	if q == 0 && f == 0 && ts == 0 && tf == 0 {
		return
	}

	if err := t.persist(q, f, ts, tf); err != nil {
		log.Printf("sense metrics: flush: %v", err)
		// Return the delta to the buffer so the next flush (write-through,
		// ticker, or Close) retries it — a transient DB lock never drops counts.
		t.lifetime.mu.Lock()
		t.lifetime.queries += q
		t.lifetime.fileReadsAvoided += f
		t.lifetime.tokensSaved += ts
		t.lifetime.textFallbackFired += tf
		t.lifetime.mu.Unlock()
	}
}

// persist upserts the lifetime deltas into sense_metrics in a single
// transaction, rolling back (and returning the error) if any statement fails so
// the caller can re-buffer the whole delta. Zero deltas are skipped.
func (t *Tracker) persist(q, f, ts, tf int64) error {
	ctx := context.Background()
	const upsert = `INSERT INTO sense_metrics (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = value + excluded.value`
	tx, err := t.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	deltas := []struct {
		key string
		val int64
	}{
		{"lifetime_queries", q},
		{"lifetime_file_reads_avoided", f},
		{"lifetime_tokens_saved", ts},
		{"lifetime_text_fallback_fired", tf},
	}
	for _, d := range deltas {
		if d.val == 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, upsert, d.key, d.val); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("upsert %s: %w", d.key, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

func (t *Tracker) loadPersisted(ctx context.Context) LifetimeCounters {
	var lc LifetimeCounters
	rows, err := t.db.QueryContext(ctx, `SELECT key, value FROM sense_metrics`)
	if err != nil {
		return lc
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var key string
		var value int
		if err := rows.Scan(&key, &value); err != nil {
			continue
		}
		switch key {
		case "lifetime_queries":
			lc.Queries = value
		case "lifetime_file_reads_avoided":
			lc.FileReadsAvoided = value
		case "lifetime_tokens_saved":
			lc.TokensSaved = value
		case "lifetime_text_fallback_fired":
			lc.TextFallbackFired = value
		}
	}
	return lc
}

// SessionCounters holds in-memory session-scoped counters.
type SessionCounters struct {
	Queries           int
	FileReadsAvoided  int
	TokensSaved       int
	TextFallbackFired int
}

// LifetimeCounters holds all-time counters (persisted + pending).
type LifetimeCounters struct {
	Queries           int
	FileReadsAvoided  int
	TokensSaved       int
	TextFallbackFired int
}

// TopQueryInfo holds the single highest-saving query this session.
type TopQueryInfo struct {
	Tool        string
	Args        string
	TokensSaved int
}
