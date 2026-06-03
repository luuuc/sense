package scan_test

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/scan/scantest"
)

// oracleRepo is the fixed working tree the behavior oracle scans. It is chosen
// to exercise every category the digest covers: cross-file call edges, a
// reflective dispatch that lands in the per-language sense_meta name-sets, and
// enough symbols that embedding presence is non-trivial. Keep it small and
// stable — changing it changes the golden digest.
var oracleRepo = map[string]string{
	"app/models/user.rb": `class User
  def greet
    "hi, #{name}"
  end

  def name
    "anon"
  end
end
`,
	"app/services/notifier.rb": `class Notifier
  def notify(user)
    user.greet
    send(:log)
  end

  def log
    "logged"
  end
end
`,
}

// oracleDigest hashes everything the scan derives — the sorted symbol set, the
// edge set, the per-language sense_meta name-sets, and embedding presence — into
// a single content fingerprint. It hashes WHAT came out, not HOW MUCH: a seam
// that swapped two edges, mislabeled a kind, or routed a harvested name into the
// wrong language's set would change the digest even though every count held.
// Volatile meta (timestamps) is excluded so the digest depends only on derived
// structure. It also returns the sorted content lines so a mismatch can report
// WHAT changed, not just that two hashes differ. Other tests in 27-03/04 reuse
// this as the net under their seams.
func oracleDigest(t *testing.T, dbPath string) (digest string, content []string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	var lines []string
	collect := func(query string, scan func(*sql.Rows) (string, error)) {
		rows, err := db.Query(query)
		if err != nil {
			t.Fatalf("oracle query %q: %v", query, err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			line, err := scan(rows)
			if err != nil {
				t.Fatalf("oracle scan %q: %v", query, err)
			}
			lines = append(lines, line)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("oracle rows %q: %v", query, err)
		}
	}

	collect(`SELECT qualified, kind FROM sense_symbols`, func(r *sql.Rows) (string, error) {
		var q, k string
		err := r.Scan(&q, &k)
		return fmt.Sprintf("S\t%s\t%s", q, k), err
	})
	collect(`SELECT IFNULL(src.qualified, ''), tgt.qualified, e.kind
	         FROM sense_edges e
	         LEFT JOIN sense_symbols src ON e.source_id = src.id
	         JOIN sense_symbols tgt ON e.target_id = tgt.id`, func(r *sql.Rows) (string, error) {
		var src, tgt, kind string
		err := r.Scan(&src, &tgt, &kind)
		return fmt.Sprintf("E\t%s\t%s\t%s", src, tgt, kind), err
	})
	collect(`SELECT key, value FROM sense_meta
	         WHERE key NOT IN ('last_scan_at', 'embedding_watermark')`, func(r *sql.Rows) (string, error) {
		var k, v string
		err := r.Scan(&k, &v)
		return fmt.Sprintf("M\t%s\t%s", k, v), err
	})
	collect(`SELECT s.qualified FROM sense_embeddings em
	         JOIN sense_symbols s ON em.symbol_id = s.id`, func(r *sql.Rows) (string, error) {
		var q string
		err := r.Scan(&q)
		return fmt.Sprintf("EMB\t%s", q), err
	})

	// Sort after collection so the digest never depends on row order, which
	// SQLite does not guarantee absent ORDER BY.
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:]), lines
}

// oracleGolden is the content fingerprint of oracleRepo. It is pinned, not
// computed at runtime: a behavior-preserving refactor (a seam, a split) must
// leave it unchanged, and a refactor that alters derived output fails here
// loudly. When output changes on purpose, regenerate it (see the capture line
// in TestScanContentOracleDigest) and update this constant in the same commit.
const oracleGolden = "e2ae251f1928556d5f19a8a195ace3c7b109d6ebd8b3337c661d1d4332ab8fcb"

func TestScanContentOracleDigest(t *testing.T) {
	useFakeEmbedder(t)
	repo := scantest.NewRepo(t, oracleRepo)
	res, _ := repo.Scan(scan.Options{EmbeddingsEnabled: true, Embed: true})
	if res.Embedded == 0 {
		t.Fatal("expected the fake embedder to produce embeddings")
	}

	got, content := oracleDigest(t, filepath.Join(repo.Root, ".sense", "index.db"))
	// Capture line: run `go test -run TestScanContentOracleDigest -v` and copy
	// the logged digest into oracleGolden when the change is intentional.
	t.Logf("oracle digest: %s", got)
	if got != oracleGolden {
		// Print the content so the failure says WHAT changed, not just that two
		// hashes differ — the diff against the prior run is where the regression is.
		t.Errorf("oracle digest changed:\n got  %s\n want %s\nIf this change is intentional, update oracleGolden. Derived content:\n%s",
			got, oracleGolden, strings.Join(content, "\n"))
	}
}

// TestScanContentOracleStable proves the digest is the net it claims to be: a
// rescan with no source change reproduces it byte-for-byte, while editing a
// file moves it. The first guards against nondeterminism; the second against a
// digest so coarse it would miss a real change.
func TestScanContentOracleStable(t *testing.T) {
	useFakeEmbedder(t)
	repo := scantest.NewRepo(t, oracleRepo)
	opts := scan.Options{EmbeddingsEnabled: true, Embed: true}

	repo.Scan(opts)
	dbPath := filepath.Join(repo.Root, ".sense", "index.db")
	first, _ := oracleDigest(t, dbPath)

	repo.Scan(opts) // rescan, no change
	if again, _ := oracleDigest(t, dbPath); again != first {
		t.Errorf("digest not stable across rescans:\n first %s\n again %s", first, again)
	}

	repo.Write("app/models/user.rb", "class User\n  def farewell\n    \"bye\"\n  end\nend\n")
	repo.Scan(opts)
	if changed, _ := oracleDigest(t, dbPath); changed == first {
		t.Error("digest unchanged after editing a source file; oracle is too coarse")
	}
}
