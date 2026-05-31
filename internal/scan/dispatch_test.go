package scan

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/luuuc/sense/internal/sqlite"
)

func openTempAdapter(t *testing.T) *sqlite.Adapter {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "index.db")
	a, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func TestWriteReadDispatchNamesRoundTrip(t *testing.T) {
	ctx := context.Background()
	a := openTempAdapter(t)

	if err := writeDispatchNames(ctx, a, map[string]struct{}{"foo": {}, "bar": {}}); err != nil {
		t.Fatalf("writeDispatchNames: %v", err)
	}
	got, err := readDispatchNames(ctx, a)
	if err != nil {
		t.Fatalf("readDispatchNames: %v", err)
	}
	for _, want := range []string{"foo", "bar"} {
		if _, ok := got[want]; !ok {
			t.Errorf("dispatch set missing %q: %v", want, got)
		}
	}
}

func TestWriteDispatchNamesUnionsWithExisting(t *testing.T) {
	ctx := context.Background()
	a := openTempAdapter(t)

	// First scan persists {foo}.
	if err := writeDispatchNames(ctx, a, map[string]struct{}{"foo": {}}); err != nil {
		t.Fatalf("write 1: %v", err)
	}
	// Incremental scan only re-walks a file contributing {bar}; foo must survive.
	if err := writeDispatchNames(ctx, a, map[string]struct{}{"bar": {}}); err != nil {
		t.Fatalf("write 2: %v", err)
	}
	got, err := readDispatchNames(ctx, a)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, want := range []string{"foo", "bar"} {
		if _, ok := got[want]; !ok {
			t.Errorf("union lost %q: %v", want, got)
		}
	}
}

func TestWriteDispatchNamesEmptyNoKey(t *testing.T) {
	ctx := context.Background()
	a := openTempAdapter(t)

	// Empty collected + nothing persisted → key stays absent.
	if err := writeDispatchNames(ctx, a, nil); err != nil {
		t.Fatalf("write: %v", err)
	}
	raw, err := a.ReadMeta(ctx, dispatchNamesMetaKey)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if raw != "" {
		t.Errorf("expected no dispatch_names key, got %q", raw)
	}
}

func TestWriteReadMentionedNamesRoundTrip(t *testing.T) {
	ctx := context.Background()
	a := openTempAdapter(t)

	if err := writeMentionedNames(ctx, a, map[string]struct{}{"foo": {}, "bar": {}}); err != nil {
		t.Fatalf("writeMentionedNames: %v", err)
	}
	got, err := readMentionedNames(ctx, a)
	if err != nil {
		t.Fatalf("readMentionedNames: %v", err)
	}
	for _, want := range []string{"foo", "bar"} {
		if _, ok := got[want]; !ok {
			t.Errorf("mention set missing %q: %v", want, got)
		}
	}
}

func TestWriteMentionedNamesUnionsWithExisting(t *testing.T) {
	ctx := context.Background()
	a := openTempAdapter(t)

	if err := writeMentionedNames(ctx, a, map[string]struct{}{"foo": {}}); err != nil {
		t.Fatalf("write 1: %v", err)
	}
	if err := writeMentionedNames(ctx, a, map[string]struct{}{"bar": {}}); err != nil {
		t.Fatalf("write 2: %v", err)
	}
	got, err := readMentionedNames(ctx, a)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, want := range []string{"foo", "bar"} {
		if _, ok := got[want]; !ok {
			t.Errorf("union lost %q: %v", want, got)
		}
	}
}

func TestWriteMentionedNamesEmptyNoKey(t *testing.T) {
	ctx := context.Background()
	a := openTempAdapter(t)

	if err := writeMentionedNames(ctx, a, nil); err != nil {
		t.Fatalf("write: %v", err)
	}
	raw, err := a.ReadMeta(ctx, mentionedNamesMetaKey)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if raw != "" {
		t.Errorf("expected no mentioned_names key, got %q", raw)
	}
}

func TestReadDispatchNamesCorruptIsEmpty(t *testing.T) {
	ctx := context.Background()
	a := openTempAdapter(t)

	if err := a.WriteMeta(ctx, dispatchNamesMetaKey, "{not valid json"); err != nil {
		t.Fatalf("seed corrupt: %v", err)
	}
	got, err := readDispatchNames(ctx, a)
	if err != nil {
		t.Fatalf("readDispatchNames: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("corrupt value should read as empty, got %v", got)
	}
}

// TestDispatchNamesAdapterErrors covers the propagated-error paths: a closed
// adapter makes the underlying meta read/write fail, and both functions must
// surface that rather than swallow it.
func TestDispatchNamesAdapterErrors(t *testing.T) {
	ctx := context.Background()
	a := openTempAdapter(t)
	_ = a.Close() // force every subsequent query to error

	if _, err := readDispatchNames(ctx, a); err == nil {
		t.Error("readDispatchNames should surface a closed-adapter error")
	}
	// writeDispatchNames reads the existing set first, so the closed adapter
	// makes it error before any write.
	if err := writeDispatchNames(ctx, a, map[string]struct{}{"foo": {}}); err == nil {
		t.Error("writeDispatchNames should surface the existing-read error")
	}
}

// TestScanWritesDispatchNames is the end-to-end proof: scanning a Ruby file
// that reflectively dispatches on a literal name persists that name to
// sense_meta, where the dead-code arbiter reads it.
func TestScanWritesDispatchNames(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	senseDir := filepath.Join(root, ".sense")

	src := "class Dispatcher\n" +
		"  def run\n" +
		"    send(:hidden_handler)\n" +
		"  end\n" +
		"end\n"
	if err := os.WriteFile(filepath.Join(root, "dispatcher.rb"), []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	if _, err := Run(ctx, Options{
		Root:     root,
		Sense:    senseDir,
		Output:   &bytes.Buffer{},
		Warnings: io.Discard,
	}); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}

	a, err := sqlite.Open(ctx, filepath.Join(senseDir, "index.db"))
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	got, err := readDispatchNames(ctx, a)
	if err != nil {
		t.Fatalf("readDispatchNames: %v", err)
	}
	if _, ok := got["hidden_handler"]; !ok {
		t.Errorf("expected 'hidden_handler' in dispatch_names meta, got %v", got)
	}
}
