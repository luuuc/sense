package blast_test

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/luuuc/sense/internal/blast"
	"github.com/luuuc/sense/internal/index"
	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/sqlite"
)

// TestEnqueueHierarchyClosure is the cross-card acceptance gate for 29-01: a
// scan of a fixture mirroring mastodon's delivery hierarchy, then a single
// blast on the terminal worker, must reach every emitter — direct, wrapped via
// an own-#perform worker, via a `super`-delegating subclass, and via a subclass
// with NO own #perform (inherited-method resolution). None of these emitters
// names the terminal worker, so grep cannot assemble them.
func TestEnqueueHierarchyClosure(t *testing.T) {
	if testing.Short() {
		t.Skip("scans a fixture; run without -short")
	}
	files := map[string]string{
		// Terminal worker — the blast subject.
		"app/workers/delivery_worker.rb": `class DeliveryWorker
  include Sidekiq::Worker
  def perform(json, inbox_url)
  end
end`,
		// Base wrapper: own #perform → distribute! → DeliveryWorker.push_bulk.
		"app/workers/raw_distribution_worker.rb": `class RawDistributionWorker
  include Sidekiq::Worker
  def perform(json, account_id)
    distribute!
  end

  def distribute!
    DeliveryWorker.push_bulk(inboxes) do |inbox_url|
      [payload, inbox_url]
    end
  end
end`,
		// Own-#perform wrapper that includes Sidekiq directly (not a subclass).
		"app/workers/move_distribution_worker.rb": `class MoveDistributionWorker
  include Sidekiq::Worker
  def perform(migration_id)
    DeliveryWorker.push_bulk(inboxes) do |inbox_url|
      [payload, inbox_url]
    end
  end
end`,
		// Subclass with its own #perform delegating via super.
		"app/workers/collection_raw_distribution_worker.rb": `class CollectionRawDistributionWorker < RawDistributionWorker
  def perform(json, collection_id)
    super(json, account_id)
  end
end`,
		// Subclass with NO own #perform — purely inherits the run method.
		"app/workers/account_raw_distribution_worker.rb": `class AccountRawDistributionWorker < RawDistributionWorker
  def inboxes
    @inboxes ||= reach
  end
end`,
		// Emitters — none references DeliveryWorker.
		"app/services/move_service.rb": `class MoveService
  def call
    MoveDistributionWorker.perform_async(migration_id)
  end
end`,
		"app/services/add_to_collection_service.rb": `class AddToCollectionService
  def call
    CollectionRawDistributionWorker.perform_async(json, collection_id)
  end
end`,
		"app/services/remove_featured_tag_service.rb": `class RemoveFeaturedTagService
  def call
    AccountRawDistributionWorker.perform_async(json, account_id)
  end
end`,
		// Direct enqueue of the base wrapper — gates the intra-class
		// perform → distribute! → push_bulk chain on its own.
		"app/services/pin_service.rb": `class PinService
  def call
    RawDistributionWorker.perform_async(json, account_id)
  end
end`,
	}

	repo := t.TempDir()
	for rel, content := range files {
		writeFixture(t, repo, rel, content)
	}

	senseDir := t.TempDir()
	ctx := context.Background()
	if _, err := scan.Run(ctx, scan.Options{
		Root:     repo,
		Sense:    senseDir,
		Output:   &bytes.Buffer{},
		Warnings: io.Discard,
	}); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}

	dbPath := filepath.Join(senseDir, "index.db")
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = adapter.Close() })

	all, err := adapter.Query(ctx, index.Filter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	idOf := map[string]int64{}
	for _, s := range all {
		idOf[s.Qualified] = s.ID
	}
	subjectID := idOf["DeliveryWorker"]
	if subjectID == 0 {
		t.Fatal("DeliveryWorker symbol missing from index")
	}

	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	out, err := blast.Compute(ctx, db, []int64{subjectID}, blast.Options{MaxHops: 6})
	if err != nil {
		t.Fatalf("blast.Compute: %v", err)
	}

	reached := map[string]bool{}
	for _, c := range out.DirectCallers {
		reached[c.Qualified] = true
	}
	for _, h := range out.IndirectCallers {
		reached[h.Symbol.Qualified] = true
	}

	// The four enqueueing service methods must all be in the blast closure.
	want := []string{
		"MoveService#call",              // own-#perform wrapper
		"AddToCollectionService#call",   // super-delegating subclass
		"RemoveFeaturedTagService#call", // no-own-#perform subclass (inherited resolution)
		"PinService#call",               // base wrapper, intra-class perform → distribute! → push_bulk
	}
	for _, q := range want {
		if !reached[q] {
			t.Errorf("blast closure missing emitter %q\nreached: %v", q, keys(reached))
		}
	}
}

func writeFixture(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
