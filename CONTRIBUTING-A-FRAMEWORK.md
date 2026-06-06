# Adding framework support to Sense

This guide walks you through adding support for a framework on top of a language
Sense already parses (Rails on Ruby, Django on Python, React on TypeScript), and
through the dead-code fine-graining that goes with it. It is written to be
followed literally, by an AI agent or a human, with no prior knowledge of the
codebase.

Framework support is **two halves of one job**:

1. **Emit the framework's edges.** A framework creates relationships no plain
   syntactic rule sees: a Rails `has_many :orders` is a `composes` edge, a
   FastAPI `@app.get("/x")` wires a route to a handler, a React component renders
   another. You teach the extractor to recognize the idiom and emit the edge.
2. **Keep the framework's invisibly-reached symbols out of the dead report.** The
   same idioms reach symbols with no source caller: a router dispatches a handler,
   a signal framework invokes a `@receiver`, an ORM callback fires
   `before_save`. The dead-code analyzer would call those symbols dead unless a
   voice knows the framework reaches them. You teach the analyzer that too.

Most contributors think of the first half and forget the second. They are the
same contribution: a framework that emits edges but leaves its handlers looking
dead is half-done. This guide covers both, in that order.

> **Prerequisite.** Framework inference needs a **full-tier** extractor (a
> bespoke package), because the idioms require more than the table-driven
> standard tier can express. If your target language is standard-tier today, read
> the full-tier section of [`CONTRIBUTING-A-LANGUAGE.md`](CONTRIBUTING-A-LANGUAGE.md)
> first. If it is already full-tier (Go, Ruby, Python, TypeScript/JS, Rust), you
> are in the right place.

> **Maintainer note.** The normative dead-code spec and the per-language
> soundness gate live in the internal `.doc/definition/` tree, which is **not**
> part of a public clone. References to `.doc/...` below are for maintainers; an
> external contributor can ignore them and a reviewer will wire them up.

### Before you start

```bash
git clone https://github.com/luuuc/sense.git
cd sense
./scripts/fetch-deps.sh --local   # ONNX runtime + model, required once
make build
```

---

## Architecture in one screen

A framework contribution touches at most these places. Keep the list in view; the
steps fill it in. The first group is the edges (Part 1), the second is the
dead-code half (Part 2).

| File | Role |
|---|---|
| `internal/extract/<lang>/framework.go` (or inline) | recognize the idiom, emit edges |
| `internal/extract/extractor.go` | the harvest-emitter interface, if a new open-world signal is needed |
| `internal/extract/testdata/<lang>/` | fixtures + goldens, including negative cases |
| `internal/scan/collector.go` | accumulate the harvested names |
| `internal/scan/scan.go` | a harness field for the accumulator |
| `internal/scan/dispatch.go` | a `sense_meta` key + a writer for the set |
| `internal/dead/arbiter.go` | a `Facts` field carrying the set into the voice |
| `internal/dead/meta_readers.go` | read the set from `sense_meta` into `Facts` |
| `internal/dead/voice_<lang>.go` | the voice that raises the open-world reason |
| `internal/dead/reasons.go` | the reason code (often registered from the voice) |

That looks like a lot. Most framework additions touch only the first three rows:
they emit an edge and (if the idiom is "another decorator / another annotation")
ride an open-world signal that **already exists**. The full round-trip in the
second group is only for a genuinely new kind of invisible reach. Part 2 has a
decision step so you do the minimum.

---

# Part 1. Emitting the framework's edges

Framework inference adds domain-specific edges on top of a language's base
extraction. Two code patterns exist; pick by size.

- **Pattern A, a separate file** (recommended for non-trivial frameworks). Python
  uses this: [`internal/extract/python/framework.go`](internal/extract/python/framework.go)
  holds all Django/FastAPI logic as methods on the same `walker`. The base
  extractor calls into framework methods at integration points; a non-match
  returns nil and base extraction continues unchanged.
- **Pattern B, inline** (for a handful of `case` branches). Ruby's `dispatchCall`
  routes on method name (`case "has_many", "belongs_to": ...`). Move to a separate
  file when it grows.

**What framework extractors emit:**

- `composes` for ORM associations and typed fields (Rails `has_many :orders`,
  Django `ForeignKey(User)`).
- `calls` for lifecycle callbacks and route-to-handler wiring (Rails
  `before_action`, FastAPI `@app.get`).
- `imports` for module inclusion.

Framework edges use `ConfidenceConvention` (0.9): they rely on naming patterns,
not syntactic proof. Use the shared confidence constants from
[`internal/extract/extractor.go`](internal/extract/extractor.go); never invent
literals.

**Cross-language edges.** Some frameworks bridge languages. The ERB extractor
connects templates to JS: `data-controller="checkout"` resolves to
`CheckoutController`, `<turbo-stream-from>` to a `turbo-channel:` prefix.
Cross-language resolution works through synthetic qualified-name prefixes defined
in `extractor.go` (`PrefixTurboChannel`, `PrefixImportmap`, ...); **both** emitter
and receiver must produce the same qualified name. The shared helper
`extract.StimulusControllerQualified()` keeps ERB and TS/JS in agreement.

**RawExtractor (non-tree-sitter languages).** Template languages like ERB have no
grammar. Implement `RawExtractor` (`ExtractRaw(source, filePath, emit) error`)
instead; when an extractor implements both, the harness calls `ExtractRaw` and
skips parsing, and `Grammar()` returns nil. See
[`internal/extract/erb/`](internal/extract/erb/).

**The `filePath` parameter** carries file-path-dependent semantics: Ruby checks
for `config/routes.rb`, Python detects `urls.py` for Django URL patterns. Store
`filePath` on the walker if your framework's meaning depends on location.

**Fixtures.** Add framework fixtures alongside the language's existing ones
(`testdata/ruby/rails_associations.rb` + `.golden.json`), and **include negative
cases**: patterns that look like framework code but should emit nothing. Generate
the golden, then read the diff before committing it:

```bash
go test ./internal/extract -run 'TestFixtures/<lang>' -update   # regenerate
go test ./internal/extract -run 'TestFixtures/<lang>'           # verify
```

`-update` will happily bless wrong output. Check that the right edges appear, with
the right kinds and confidences, and that negative cases stay empty.

---

# Part 2. Dead-code fine-graining

A framework reaches symbols the static call graph cannot see. Without this half,
those symbols show up as false positives in `sense dead`. This is where you teach
the analyzer the framework's invisible-reach idioms.

## The one invariant that makes this safe

The dead-code analyzer runs a panel of **voices**
([`internal/dead/arbiter.go`](internal/dead/arbiter.go)). A voice answers one
question about a candidate symbol: *"could a hidden caller exist?"* It may only
ever **raise its hand**: return a `Reason` to push a symbol to `possibly_dead`.
No voice can vote *for* `dead`. A symbol earns `dead` only when **every** voice
stays silent **and** a language voice for its language is registered (so
closed-world is provable) **and** the soundness gate passes.

```go
type Voice interface {
    Lang() string                          // "" = applies to all languages
    Inspect(s Symbol, f Facts) *Reason     // a reason, or nil for "no risk I see"
}
```

This is why adding framework knowledge is safe: the worst a wrong voice can do is
keep a symbol open-world (recall loss), never falsely call something dead
(soundness loss). Always err toward raising a hand.

## Step 1. Decide how much wiring you actually need

Three cases, cheapest first. Do not build more than your framework needs.

**Case A: an existing open-world signal already covers it.** The harvested signals
are deliberately broad. Python harvests *every decorated symbol* as
`PythonDecoratedName` (reason `py_decorator`); langspec harvests *every annotated
symbol* as `LangspecAnnotatedName` (reason `ls_annotated`). If your framework's
idiom is "this symbol carries a decorator/annotation", the **extractor already
emits the harvest name** and the voice already raises a hand. You may have
nothing to do in Part 2. Verify against a real repo (Step 4) before assuming so.

**Case B: a new, more specific reason on an existing harvest.** Your idiom is
already harvested broadly, but you want a precise hint. Example: Flask routes are
decorated (so `py_decorator` already keeps them open-world), but `py_route` tells
the user *why* with a routing-specific message. This adds a harvest fact and a
reason, but no new voice. This is the full round-trip in Step 2.

**Case C: a brand-new invisible-reach idiom** the language voice cannot express
with existing facts. Same round-trip as Case B, and possibly a new check inside
the voice. This is rare; the existing voices cover most idioms.

If you are in Case A, skip to Step 4. Cases B and C use Step 2 and Step 3.

## Step 2. The harvest round-trip

A harvested fact is a project-wide set of names the scan collects once and the
voice reads. The set flows extract -> scan -> `sense_meta` -> dead. Adding one
means touching each layer. Use the Python decorated-name path as the worked
example to copy; every file below already contains it.

1. **Declare the emitter method** on the language's harvest interface in
   [`internal/extract/extractor.go`](internal/extract/extractor.go). Example: the
   `PythonHarvestEmitter` interface declares `PythonRouteName(name string) error`.
   The interface doc comment is the normative description of what each name means;
   write yours there.

2. **Emit the name** from the extractor, in `framework.go`, when you recognize the
   idiom. Probe the emitter with a type assertion so an emitter that does not
   implement the interface simply receives nothing:

   ```go
   if h, ok := w.emit.(extract.PythonHarvestEmitter); ok {
       _ = h.PythonRouteName(handlerName)
   }
   ```

3. **Accumulate it** in [`internal/scan/collector.go`](internal/scan/collector.go).
   The `collector` implements the harvest-emitter interfaces; add the method that
   appends to a slice:

   ```go
   func (c *collector) PythonRouteName(name string) error {
       c.pyRoutes = append(c.pyRoutes, name)
       return nil
   }
   ```

4. **Hold the project-wide set** with a harness field in
   [`internal/scan/scan.go`](internal/scan/scan.go) (e.g. `pyRoutes map[string]struct{}`),
   folded in by `partitionHarvestedNames`.

5. **Persist it** in [`internal/scan/dispatch.go`](internal/scan/dispatch.go): a
   `sense_meta` key const and a writer, called from `writeHarvestedMeta`:

   ```go
   const pythonRoutesMetaKey = "py_routes"
   func writePythonRoutes(ctx context.Context, idx *sqlite.Adapter, c map[string]struct{}) error {
       return writeNameSet(ctx, idx, pythonRoutesMetaKey, c)
   }
   ```

6. **Carry it into the voice** with a `Facts` field in
   [`internal/dead/arbiter.go`](internal/dead/arbiter.go) (e.g.
   `PythonRouteNames map[string]struct{}`). Document it where the other fields are
   documented; the field comment is part of the contract.

7. **Read it** in [`internal/dead/meta_readers.go`](internal/dead/meta_readers.go),
   inside `buildFacts`, via `readStringSetMeta(ctx, db, "py_routes")` (flat key) or
   `readNameSetMetaByLang` (per-language key).

8. **Consume it and raise a reason** in the voice
   ([`internal/dead/voice_python.go`](internal/dead/voice_python.go)): if the
   symbol's name is in the set, return the reason. Order checks most-live-first so
   the most specific hint wins.

9. **Register the reason** in [`internal/dead/reasons.go`](internal/dead/reasons.go),
   usually from the voice file's `init()` via `registerReasons`. A reason carries a
   `priority` (the arbiter picks the lowest across voices), a one-line `hint`, and
   a fuller `verify` recipe telling the user how to confirm before deleting.

> **Why so spread out?** Each step is small and the pattern is identical to the
> rows already there, but the round-trip crosses three packages
> (`extract` emits, `scan` persists, `dead` reads) and that is intentional:
> extractors are stateless and database-free, so a fact they discover has to be
> carried to the analyzer through `sense_meta` rather than recomputed. Copy an
> existing fact end-to-end rather than inventing a new shape. *(Maintainers: this
> per-fact boilerplate is a known rough edge; a `harvestFact{key, reader}` table
> could collapse steps 5 and 7. Not done yet; do not block a contribution on it.)*

## Step 3. Register a new language voice (only if there is none)

If you are adding the **first** framework for a language that has no voice yet,
register one. A voice is a small struct with `Lang()` and `Inspect()`, plus its
reasons. Add it to the panel in
[`internal/dead/dead.go`](internal/dead/dead.go) (`defaultArbiter`): a one-line
addition to the `NewArbiter(...)` call. Read
[`internal/dead/voice_go.go`](internal/dead/voice_go.go) as the cleanest complete
example, and the langspec voice for the table-driven case.

A non-empty `Lang()` does two things at once: it scopes the voice to that
language's symbols, **and** it declares that Sense can prove closed-world for the
language (so its symbols become eligible for `dead` at all). Do not register a
voice for a language whose dead verdict you have not validated against a real
repo.

## Step 4. Validate against a real repo, not a fixture

Goldens prove the extractor emits what you wrote; they cannot prove the dead
verdict is *right*, because rightness is a property of real code. Point Sense at a
real project on the framework and read the dead report by hand.

```bash
cd /path/to/a/real/<framework>/app
sense scan                      # scan-layer facts need a fresh scan to take effect
sense dead --language <lang>
```

Confirm two things: framework-reached symbols (routes, callbacks, signal
handlers) are **not** in the `dead` list (they should be `possibly_dead` with your
reason), and genuinely-unused code still **is**. A maintainer keeps reference
repos for this (for example, a Rails app validates the Ruby/Rails voices); ask in
the PR which repo to validate against if you do not have one.

> **Scan-layer vs query-layer.** Anything you change in `extract` or `scan`
> (emitting a harvest name, persisting a `sense_meta` key) only takes effect after
> `rm -rf .sense && sense scan`. Changes confined to `dead` (a voice raising a new
> reason from facts already in `sense_meta`) take effect against the existing
> index immediately. Knowing which layer you touched saves a rescan.

## Step 5. Cover your logic and run the gates

A bespoke voice is real logic held to the per-file coverage floor (see
[`CONTRIBUTING.md`](CONTRIBUTING.md)). Unit-test the voice's `Inspect` directly
against constructed `Symbol`/`Facts` values (the existing `voice_*_test.go` files
show the pattern), not only through end-to-end scans. Then:

```bash
go test ./internal/extract/... ./internal/scan/... ./internal/dead/...
make ci
make smoke
```

---

## Reference: existing framework support

- **Ruby/Rails** ([`internal/extract/ruby/`](internal/extract/ruby/),
  [`internal/dead/voice_rails.go`](internal/dead/voice_rails.go)), covering associations,
  callbacks, routes, Stimulus/Turbo. The fullest example.
- **Python/Django+FastAPI** ([`internal/extract/python/framework.go`](internal/extract/python/framework.go),
  [`internal/dead/voice_python.go`](internal/dead/voice_python.go)), covering decorators,
  routes, signal handlers, `__all__`. The cleanest harvest round-trip to copy.
- **TypeScript/React** ([`internal/extract/tsjs/`](internal/extract/tsjs/),
  [`internal/dead/voice_ts.go`](internal/dead/voice_ts.go)), covering JSX components,
  decorators, default exports.
- **ERB cross-language** ([`internal/extract/erb/`](internal/extract/erb/)), covering
  template-to-JS via synthetic prefixes, the `RawExtractor` path.

---

## When you get stuck

If a step here does not match what you see in the repo, the **doc** is wrong, not
you. Open an issue or PR against this file. This guide is meant to be followable
end-to-end with zero gaps; a gap a newcomer hits is a bug in the guide.
