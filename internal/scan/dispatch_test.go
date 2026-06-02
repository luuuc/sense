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

	if err := writeDispatchNames(ctx, a, "ruby", map[string]struct{}{"foo": {}, "bar": {}}); err != nil {
		t.Fatalf("writeDispatchNames: %v", err)
	}
	got, err := readDispatchNames(ctx, a, "ruby")
	if err != nil {
		t.Fatalf("readDispatchNames: %v", err)
	}
	for _, want := range []string{"foo", "bar"} {
		if _, ok := got[want]; !ok {
			t.Errorf("dispatch set missing %q: %v", want, got)
		}
	}
	// The set is persisted under the per-language key, not the bare union key,
	// so the dead-code reader's glob discovers the language and a legacy index
	// (bare key only) fails closed.
	if raw, _ := a.ReadMeta(ctx, "dispatch_names:ruby"); raw == "" {
		t.Error("expected dispatch_names:ruby key to be written")
	}
	if raw, _ := a.ReadMeta(ctx, dispatchNamesMetaKey); raw != "" {
		t.Errorf("bare union key must not be written, got %q", raw)
	}
	// A different language's set is independent.
	if other, _ := readDispatchNames(ctx, a, "go"); len(other) != 0 {
		t.Errorf("go dispatch set should be empty, got %v", other)
	}
}

func TestWriteDispatchNamesUnionsWithExisting(t *testing.T) {
	ctx := context.Background()
	a := openTempAdapter(t)

	// First scan persists {foo}.
	if err := writeDispatchNames(ctx, a, "ruby", map[string]struct{}{"foo": {}}); err != nil {
		t.Fatalf("write 1: %v", err)
	}
	// Incremental scan only re-walks a file contributing {bar}; foo must survive.
	if err := writeDispatchNames(ctx, a, "ruby", map[string]struct{}{"bar": {}}); err != nil {
		t.Fatalf("write 2: %v", err)
	}
	got, err := readDispatchNames(ctx, a, "ruby")
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
	if err := writeDispatchNames(ctx, a, "ruby", nil); err != nil {
		t.Fatalf("write: %v", err)
	}
	raw, err := a.ReadMeta(ctx, dispatchNamesKey("ruby"))
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if raw != "" {
		t.Errorf("expected no dispatch_names:ruby key, got %q", raw)
	}
}

// TestWarnMetaWrite pins the graceful-degradation contract: a name-set
// meta-write failure is reported as a warning and does not abort the scan,
// while a successful write (nil err) is silent. The scan must keep the index
// it already wrote even when the dead-code recall signal fails to persist.
func TestWarnMetaWrite(t *testing.T) {
	var buf bytes.Buffer

	warnMetaWrite(&buf, "dispatch-names", nil)
	if buf.Len() != 0 {
		t.Errorf("a successful write must be silent, got %q", buf.String())
	}

	warnMetaWrite(&buf, "mentioned-names", errForcedMeta)
	got := buf.String()
	if !bytes.Contains(buf.Bytes(), []byte("warn: write mentioned-names meta:")) {
		t.Errorf("a failed write must warn, got %q", got)
	}
}

var errForcedMeta = errForced{}

type errForced struct{}

func (errForced) Error() string { return "forced meta write failure" }

func TestWriteReadMentionedNamesRoundTrip(t *testing.T) {
	ctx := context.Background()
	a := openTempAdapter(t)

	if err := writeMentionedNames(ctx, a, "ruby", map[string]struct{}{"foo": {}, "bar": {}}); err != nil {
		t.Fatalf("writeMentionedNames: %v", err)
	}
	got, err := readMentionedNames(ctx, a, "ruby")
	if err != nil {
		t.Fatalf("readMentionedNames: %v", err)
	}
	for _, want := range []string{"foo", "bar"} {
		if _, ok := got[want]; !ok {
			t.Errorf("mention set missing %q: %v", want, got)
		}
	}
	if raw, _ := a.ReadMeta(ctx, "mentioned_names:ruby"); raw == "" {
		t.Error("expected mentioned_names:ruby key to be written")
	}
	if raw, _ := a.ReadMeta(ctx, mentionedNamesMetaKey); raw != "" {
		t.Errorf("bare union key must not be written, got %q", raw)
	}
}

func TestWriteMentionedNamesUnionsWithExisting(t *testing.T) {
	ctx := context.Background()
	a := openTempAdapter(t)

	if err := writeMentionedNames(ctx, a, "ruby", map[string]struct{}{"foo": {}}); err != nil {
		t.Fatalf("write 1: %v", err)
	}
	if err := writeMentionedNames(ctx, a, "ruby", map[string]struct{}{"bar": {}}); err != nil {
		t.Fatalf("write 2: %v", err)
	}
	got, err := readMentionedNames(ctx, a, "ruby")
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

	if err := writeMentionedNames(ctx, a, "ruby", nil); err != nil {
		t.Fatalf("write: %v", err)
	}
	raw, err := a.ReadMeta(ctx, mentionedNamesKey("ruby"))
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if raw != "" {
		t.Errorf("expected no mentioned_names:ruby key, got %q", raw)
	}
}

func TestReadDispatchNamesCorruptIsEmpty(t *testing.T) {
	ctx := context.Background()
	a := openTempAdapter(t)

	if err := a.WriteMeta(ctx, dispatchNamesKey("ruby"), "{not valid json"); err != nil {
		t.Fatalf("seed corrupt: %v", err)
	}
	got, err := readDispatchNames(ctx, a, "ruby")
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

	if _, err := readDispatchNames(ctx, a, "ruby"); err == nil {
		t.Error("readDispatchNames should surface a closed-adapter error")
	}
	// writeDispatchNames reads the existing set first, so the closed adapter
	// makes it error before any write.
	if err := writeDispatchNames(ctx, a, "ruby", map[string]struct{}{"foo": {}}); err == nil {
		t.Error("writeDispatchNames should surface the existing-read error")
	}
}

// TestScanWritesHarvestedLangs proves the harvested-langs capability gate
// end to end: Ruby, Go, Python, and Java files mark their languages harvested
// (Java is the one langspec language wired with MentionKinds, validated against
// the javalin benchmark), while a C# file in the same scan does NOT mark `csharp`
// — the generic-spec C# extractor harvests no mentions (no benchmark repo), so its
// symbols must fail closed rather than earn `dead` off an absent set. This is what
// lets HarvestedLangs diverge from the mention keyset for a real, non-harvesting
// language.
func TestScanWritesHarvestedLangs(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	senseDir := filepath.Join(root, ".sense")

	if err := os.WriteFile(filepath.Join(root, "thing.rb"),
		[]byte("class Thing\n  def go\n    helper\n  end\nend\n"), 0o644); err != nil {
		t.Fatalf("write ruby: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"),
		[]byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "thing.py"),
		[]byte("def helper():\n    pass\n"), 0o644); err != nil {
		t.Fatalf("write python: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "Thing.java"),
		[]byte("class Thing {\n  void go() {}\n}\n"), 0o644); err != nil {
		t.Fatalf("write java: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "Thing.cs"),
		[]byte("class Thing {\n  void Go() {}\n}\n"), 0o644); err != nil {
		t.Fatalf("write csharp: %v", err)
	}

	if _, err := Run(ctx, Options{Root: root, Sense: senseDir, Output: &bytes.Buffer{}, Warnings: io.Discard}); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}

	a, err := sqlite.Open(ctx, filepath.Join(senseDir, "index.db"))
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	got, err := readNameSet(ctx, a, harvestedLangsMetaKey)
	if err != nil {
		t.Fatalf("read harvested_langs: %v", err)
	}
	for _, lang := range []string{"ruby", "go", "python", "java"} {
		if _, ok := got[lang]; !ok {
			t.Errorf("expected %s in harvested_langs, got %v", lang, got)
		}
	}
	if _, ok := got["csharp"]; ok {
		t.Errorf("csharp (no benchmark, no MentionKinds) harvests no mentions; must be absent from harvested_langs, got %v", got)
	}
}

// TestScanWritesCgoExports proves the cgo-export harvest flows end to end: a Go
// file with a `//export` directive lands its function name in the cgo_exports
// sense_meta key, where the dead-code Go voice reads it to keep the function
// open-world (it is called from C, never by a Go caller).
func TestScanWritesCgoExports(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	senseDir := filepath.Join(root, ".sense")

	if err := os.WriteFile(filepath.Join(root, "bridge.go"),
		[]byte("package main\n\n//export GoCallback\nfunc GoCallback() {}\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write go: %v", err)
	}

	if _, err := Run(ctx, Options{Root: root, Sense: senseDir, Output: &bytes.Buffer{}, Warnings: io.Discard}); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}

	a, err := sqlite.Open(ctx, filepath.Join(senseDir, "index.db"))
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	got, err := readNameSet(ctx, a, cgoExportsMetaKey)
	if err != nil {
		t.Fatalf("read cgo_exports: %v", err)
	}
	if _, ok := got["GoCallback"]; !ok {
		t.Errorf("expected GoCallback in cgo_exports, got %v", got)
	}
}

// TestScanWritesRustHarvest proves the Rust attribute harvest flows end to end
// through the scan collector and aggregation: a single Rust file carrying every
// attribute shape lands each name in its sense_meta key, where the dead-code Rust
// voice reads it. It exercises all four RustHarvestEmitter collector paths in one
// scan.
func TestScanWritesRustHarvest(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	senseDir := filepath.Join(root, ".sense")

	src := `#[no_mangle]
pub extern "C" fn ffi_entry() {}

#[used]
static KEEP: u8 = 0;

#[allow(dead_code)]
fn intentionally_kept() {}

#[cfg(test)]
mod tests {
    #[tokio::test]
    async fn async_case() {}
}

trait Greeter {
    fn greet(&self);
}

struct Robot;

impl Greeter for Robot {
    fn greet(&self) {}
}
`
	if err := os.WriteFile(filepath.Join(root, "lib.rs"), []byte(src), 0o644); err != nil {
		t.Fatalf("write rust: %v", err)
	}

	if _, err := Run(ctx, Options{Root: root, Sense: senseDir, Output: &bytes.Buffer{}, Warnings: io.Discard}); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}

	a, err := sqlite.Open(ctx, filepath.Join(senseDir, "index.db"))
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	cases := []struct {
		key, name string
	}{
		{rustExportsMetaKey, "ffi_entry"},
		{rustExportsMetaKey, "KEEP"},
		{rustTestSymbolsMetaKey, "async_case"},
		{rustAllowDeadMetaKey, "intentionally_kept"},
		{rustTraitImplMethodsMetaKey, "greet"},
	}
	for _, c := range cases {
		set, err := readNameSet(ctx, a, c.key)
		if err != nil {
			t.Fatalf("read %s: %v", c.key, err)
		}
		if _, ok := set[c.name]; !ok {
			t.Errorf("expected %q in %s, got %v", c.name, c.key, set)
		}
	}
}

// TestScanWritesTSHarvest proves the TS/JS harvest flows end to end through the
// scan collector and aggregation: a TypeScript file carrying a decorator, a
// default export, a computed-property dispatch, and a method call lands each name
// in its sense_meta key, and `typescript` is recorded as harvested. It exercises
// the TSHarvestEmitter collector paths plus the shared mention/dispatch harvest in
// one scan.
func TestScanWritesTSHarvest(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	senseDir := filepath.Join(root, ".sense")

	src := `@Component({})
export class AppComponent {
  render(obj: any) {
    obj["serialize"]();
    this.helper();
  }
  helper() {}
}

export default function Page() {}
`
	if err := os.WriteFile(filepath.Join(root, "app.ts"), []byte(src), 0o644); err != nil {
		t.Fatalf("write ts: %v", err)
	}

	if _, err := Run(ctx, Options{Root: root, Sense: senseDir, Output: &bytes.Buffer{}, Warnings: io.Discard}); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}

	a, err := sqlite.Open(ctx, filepath.Join(senseDir, "index.db"))
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	cases := []struct {
		key, name string
	}{
		{tsDecoratedMetaKey, "AppComponent"},
		{tsDefaultExportsMetaKey, "Page"},
		{dispatchNamesKey("typescript"), "serialize"},
		{mentionedNamesKey("typescript"), "helper"},
	}
	for _, c := range cases {
		set, err := readNameSet(ctx, a, c.key)
		if err != nil {
			t.Fatalf("read %s: %v", c.key, err)
		}
		if _, ok := set[c.name]; !ok {
			t.Errorf("expected %q in %s, got %v", c.name, c.key, set)
		}
	}
	// typescript registers as harvested so its symbols can earn `dead`.
	harvested, err := readNameSet(ctx, a, harvestedLangsMetaKey)
	if err != nil {
		t.Fatalf("read harvested_langs: %v", err)
	}
	if _, ok := harvested["typescript"]; !ok {
		t.Errorf("expected typescript in harvested_langs, got %v", harvested)
	}
}

// TestScanWritesLangspecAnnotated proves the langspec annotation harvest flows
// end to end through the scan collector: an annotated Java class and method land
// their names in the flat langspec_annotated sense_meta key, while a plain method
// does not. It exercises the LangspecHarvestEmitter collector path plus the
// harness aggregation in one scan.
func TestScanWritesLangspecAnnotated(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	senseDir := filepath.Join(root, ".sense")

	src := `@Service
public class Svc {
    @Test public void shouldWork() {}
    public void plain() {}
}
`
	if err := os.WriteFile(filepath.Join(root, "Svc.java"), []byte(src), 0o644); err != nil {
		t.Fatalf("write java: %v", err)
	}

	if _, err := Run(ctx, Options{Root: root, Sense: senseDir, Output: &bytes.Buffer{}, Warnings: io.Discard}); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}

	a, err := sqlite.Open(ctx, filepath.Join(senseDir, "index.db"))
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	set, err := readNameSet(ctx, a, langspecAnnotatedMetaKey)
	if err != nil {
		t.Fatalf("read langspec_annotated: %v", err)
	}
	for _, name := range []string{"Svc", "shouldWork"} {
		if _, ok := set[name]; !ok {
			t.Errorf("expected %q in langspec_annotated, got %v", name, set)
		}
	}
	if _, ok := set["plain"]; ok {
		t.Errorf("plain (un-annotated) method must not be in langspec_annotated, got %v", set)
	}
}

// TestWriteReadTSHarvestRoundTrip pins the flat TS meta keys' write/read contract,
// mirroring the dispatch/mention round-trips: a written set reads back, and an
// empty set leaves the key absent (self-heals on the next scan).
func TestWriteReadTSHarvestRoundTrip(t *testing.T) {
	ctx := context.Background()
	a := openTempAdapter(t)

	if err := writeTSDecorated(ctx, a, map[string]struct{}{"AppComponent": {}}); err != nil {
		t.Fatalf("writeTSDecorated: %v", err)
	}
	if err := writeTSDefaultExports(ctx, a, map[string]struct{}{"Page": {}}); err != nil {
		t.Fatalf("writeTSDefaultExports: %v", err)
	}
	dec, _ := readNameSet(ctx, a, tsDecoratedMetaKey)
	if _, ok := dec["AppComponent"]; !ok {
		t.Errorf("ts_decorated missing AppComponent: %v", dec)
	}
	def, _ := readNameSet(ctx, a, tsDefaultExportsMetaKey)
	if _, ok := def["Page"]; !ok {
		t.Errorf("ts_default_exports missing Page: %v", def)
	}

	// Empty collected + nothing persisted → key stays absent.
	a2 := openTempAdapter(t)
	if err := writeTSDecorated(ctx, a2, nil); err != nil {
		t.Fatalf("writeTSDecorated empty: %v", err)
	}
	if raw, _ := a2.ReadMeta(ctx, tsDecoratedMetaKey); raw != "" {
		t.Errorf("expected no ts_decorated key for empty set, got %q", raw)
	}
}

// TestScanDeletesLegacyUnionKeys proves the in-place-upgrade cleanup: a scan
// removes the stale bare `mentioned_names`/`dispatch_names` union keys a
// pre-feature index left behind, while the per-language keys survive.
func TestScanDeletesLegacyUnionKeys(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	senseDir := filepath.Join(root, ".sense")
	if err := os.WriteFile(filepath.Join(root, "thing.rb"),
		[]byte("class Thing\n  def go\n    helper\n  end\nend\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Run(ctx, Options{Root: root, Sense: senseDir, Output: &bytes.Buffer{}, Warnings: io.Discard}); err != nil {
		t.Fatalf("scan 1: %v", err)
	}

	// Simulate a pre-feature index leaving bare union keys behind.
	a, err := sqlite.Open(ctx, filepath.Join(senseDir, "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := a.WriteMeta(ctx, mentionedNamesMetaKey, `["legacy"]`); err != nil {
		t.Fatalf("seed mentioned: %v", err)
	}
	if err := a.WriteMeta(ctx, dispatchNamesMetaKey, `["legacy"]`); err != nil {
		t.Fatalf("seed dispatch: %v", err)
	}
	_ = a.Close()

	// A subsequent scan removes the stale bare keys.
	if _, err := Run(ctx, Options{Root: root, Sense: senseDir, Output: &bytes.Buffer{}, Warnings: io.Discard}); err != nil {
		t.Fatalf("scan 2: %v", err)
	}

	a2, err := sqlite.Open(ctx, filepath.Join(senseDir, "index.db"))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = a2.Close() })
	if raw, _ := a2.ReadMeta(ctx, mentionedNamesMetaKey); raw != "" {
		t.Errorf("legacy mentioned_names should be deleted, got %q", raw)
	}
	if raw, _ := a2.ReadMeta(ctx, dispatchNamesMetaKey); raw != "" {
		t.Errorf("legacy dispatch_names should be deleted, got %q", raw)
	}
	// The per-language key survives the cleanup.
	if raw, _ := a2.ReadMeta(ctx, mentionedNamesKey("ruby")); raw == "" {
		t.Error("per-language mentioned_names:ruby should remain")
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

	got, err := readDispatchNames(ctx, a, "ruby")
	if err != nil {
		t.Fatalf("readDispatchNames: %v", err)
	}
	if _, ok := got["hidden_handler"]; !ok {
		t.Errorf("expected 'hidden_handler' in dispatch_names:ruby meta, got %v", got)
	}
}

// TestScanWritesPythonHarvest proves the Python dead-code harvest flows end to
// end: a Python file with a route decorator, a generic decorator, a `@receiver`
// signal handler, and an `__all__` export lands each name in its flat sense_meta
// key, where the dead-code Python voice reads them to keep the symbols
// open-world. It also confirms python is recorded as a mention-harvesting
// language.
func TestScanWritesPythonHarvest(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	senseDir := filepath.Join(root, ".sense")

	src := `__all__ = ["_exported"]


@app.route("/health")
def health():
    return "ok"


@property
def label(self):
    return self._owner


@receiver(post_save)
def on_save(sender, **kwargs):
    return None


def _exported():
    return 1
`
	if err := os.WriteFile(filepath.Join(root, "app.py"), []byte(src), 0o644); err != nil {
		t.Fatalf("write python: %v", err)
	}

	if _, err := Run(ctx, Options{Root: root, Sense: senseDir, Output: &bytes.Buffer{}, Warnings: io.Discard}); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}

	a, err := sqlite.Open(ctx, filepath.Join(senseDir, "index.db"))
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	cases := []struct {
		key  string
		want string
	}{
		{pythonRoutesMetaKey, "health"},
		{pythonDecoratedMetaKey, "label"},
		{pythonDjangoMetaKey, "on_save"},
		{pythonAllExportsMetaKey, "_exported"},
	}
	for _, c := range cases {
		got, err := readNameSet(ctx, a, c.key)
		if err != nil {
			t.Fatalf("read %s: %v", c.key, err)
		}
		if _, ok := got[c.want]; !ok {
			t.Errorf("expected %q in %s, got %v", c.want, c.key, got)
		}
	}

	// Route and Django handlers are also in the generic decorated set (a name can
	// be in several sets; the voice reads the most specific).
	decorated, _ := readNameSet(ctx, a, pythonDecoratedMetaKey)
	for _, name := range []string{"health", "on_save"} {
		if _, ok := decorated[name]; !ok {
			t.Errorf("expected %q in %s (superset), got %v", name, pythonDecoratedMetaKey, decorated)
		}
	}
}
