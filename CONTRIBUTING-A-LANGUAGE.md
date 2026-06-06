# Adding a language to Sense

This guide walks you through adding support for a new programming language — or a
new framework on top of an existing one — from zero to a tested, registered
extractor. It is written to be followed literally, by an AI agent or a human,
with no prior knowledge of the codebase.

For *what* each support tier extracts (the conceptual model), see the
[Language Support](README.md#language-support) section of the README. This guide
is the *how*. It does not restate the tier definitions — read that section first
if "standard tier" and "full tier" are not yet meaningful to you.

> **Maintainer note.** The normative per-language spec lives in
> `.doc/definition/05-languages.md`, part of the internal design-docs tree that
> is **not** included in a public clone. References to `.doc/…` below are for
> maintainers working in a full checkout; an external contributor can ignore
> them and a reviewer will wire the definition doc up.

There are two paths:

- **Standard tier** — the easy path. A ~30-line table-driven declaration in the
  shared `langspec` extractor. Symbols, calls, inheritance, imports. Most
  languages belong here. **Start here unless you know you need more.**
- **Full tier** — a bespoke extractor in its own package, for languages that
  need type inference or framework-specific edges (Rails associations, React
  JSX, Django routes). ~500–1000 lines; documented here and grounded in the
  existing full-tier extractors as worked examples.

The single best predictor of effort is **grammar vendoring**, not the
declaration. Budget your time there — and read the vendoring pre-checks in
standard-tier Step 1 before you pick a language, because they are where a
contributor actually loses days.

---

## Architecture in one screen

Every file Sense indexes flows through the same pipeline:

1. **Walk** — the scan harness matches each file extension to a registered
   extractor via `extract.ForExtension()`. No extractor ⇒ the file is skipped.
2. **Parse** — tree-sitter parses the source into a concrete syntax tree (CST).
3. **Extract** — the language extractor walks the CST and emits symbols and
   edges through a callback interface.
4. **Resolve** — a cross-file pass maps qualified-name edges to numeric symbol
   IDs.
5. **Post-passes** — interface satisfaction, test association, embeddings.

Extractors are **stateless and per-file**: they never read other files or query
the database. Cross-file relationships are the resolver's job.

A new language touches at most these places. Keep the list in view; the steps
fill it in:

| File | Standard tier | Full tier |
|---|---|---|
| `internal/grammars/<lang>.go` | ✅ grammar constructor | ✅ grammar constructor |
| `internal/grammars/grammars_test.go` | ✅ parse smoke case | ✅ parse smoke case |
| `internal/extract/langspec/<lang>.go` | ✅ the declaration | — |
| `internal/extract/<lang>/` (new package) | — | ✅ the extractor |
| `internal/extract/extractor.go` (`languageTiers`) | ✅ one line | ✅ one line |
| `internal/extract/languages/languages.go` | — *(already imported via langspec)* | ✅ blank-import |
| `internal/extract/testdata/<lang>/` | ✅ fixtures + goldens | ✅ fixtures + goldens |
| `.doc/definition/05-languages.md` | optional | ✅ a per-language entry |

Note the one asymmetry that trips people up: a **standard-tier** language lives
*inside* the `langspec` package, which `languages.go` already blank-imports, so
its `init()` runs automatically — you do **not** add a line to `languages.go`. A
**full-tier** language is its own package, so it does.

### Before you start

```bash
git clone https://github.com/luuuc/sense.git
cd sense
./scripts/fetch-deps.sh --local   # ONNX runtime + model, required once
make build
```

---

## Standard tier (the easy path)

### Step 1 — vendor the tree-sitter grammar

Sense bundles every grammar into its binary through the Go module + CGO path
(not `go:embed` — see the package doc in
[`internal/grammars/grammars.go`](internal/grammars/grammars.go)). Vendoring a
grammar is therefore a Go dependency plus a one-line constructor — *if* the
grammar cooperates. Two pre-checks decide whether this language is a quick add
or a multi-day yak-shave. Do them **before** writing any code.

**Pre-check A — find the module, and confirm it ships a Go binding.** Many
grammars live under `github.com/tree-sitter/tree-sitter-<lang>`, but not all:
Lua is `github.com/tree-sitter-grammars/tree-sitter-lua`, Kotlin is
`github.com/fwcd/tree-sitter-kotlin`, and others live under a maintainer's fork.
Whatever the path, the module **must** ship a Go binding (a `bindings/go`
directory exposing a `Language()` function) and your constructor's import path
must match the module's declared path. No Go binding ⇒ this is not a quick add.

**Pre-check B — confirm the module vendors a generated `src/parser.c`.** This is
the failure that actually eats days. The Go binding compiles the grammar via a
CGO `#include "../../src/parser.c"`. Some grammars (e.g. several Swift bindings)
ship only `grammar.json` + `scanner.c` and *generate* `parser.c` at build time
with the tree-sitter CLI — they will **not** link through `go build`, failing
with `parser.c: file not found`. Verify the vendored file exists before
committing to the language:

```bash
go get github.com/tree-sitter/tree-sitter-<lang>@latest   # pin to a real tag
ls "$(go env GOMODCACHE)"/github.com/tree-sitter/tree-sitter-<lang>@*/src/parser.c
```

If that `ls` finds nothing, pick another language — fixing upstream packaging
will blow your budget. **One exception to watch:** a *multi-grammar* repo vendors
each grammar under `grammars/<name>/src/parser.c` (not a top-level `src/`) and
its binding exposes a named function like `LanguageOCaml()` rather than
`Language()`. The literal `ls` above will report missing and the constructor
template below won't compile as written — but the grammar is fine. Before
rejecting, open `bindings/go/*.go` in the module cache: it shows the real
`#include` path and the exported function name. Adapt the constructor to match.

With both pre-checks green, write the constructor. Create
`internal/grammars/<lang>.go` — one file, one grammar, mirroring
[`internal/grammars/java.go`](internal/grammars/java.go):

```go
package grammars

import (
    sitter "github.com/tree-sitter/go-tree-sitter"
    ts_lua "github.com/tree-sitter-grammars/tree-sitter-lua/bindings/go"
)

func Lua() *sitter.Language { return sitter.NewLanguage(ts_lua.Language()) }
```

Extractors import *this* constructor, never the upstream module directly — that
keeps the grammar set discoverable in one package. Pin to a tag, not a moving
branch: `go.mod` is the single source of truth for grammar versions.

Add a parse smoke case to
[`internal/grammars/grammars_test.go`](internal/grammars/grammars_test.go), in
the `cases` slice of `TestAllGrammarsParse`:

```go
{"lua", Lua, "function f() end\n"},
```

This catches ABI drift between the tree-sitter runtime and the grammar
(`MIN_COMPATIBLE_LANGUAGE_VERSION`) at test time instead of at first scan. Then
verify the grammar links and parses:

```bash
go test ./internal/grammars/
```

**Grammar gate.** Before going further, confirm the grammar parses *modern*
syntax for your language: feed it a snippet using recent features (C# 10
file-scoped namespaces, Python 3.12 type-parameter syntax, …) and check the CST
has no `ERROR` nodes (the s-expression dump in Step 2 shows them). A grammar that
chokes on modern syntax will silently drop real code — pick a better-maintained
one.

### Step 2 — discover the node kinds

The `langspec` declaration is a table of tree-sitter **node kind** strings
(`"method_declaration"`, `"class_definition"`, …). You cannot guess these; they
are grammar-specific. Print the parse tree of a representative snippet and read
the kinds off it.

The quickest way is a throwaway test that dumps the s-expression. Drop this into
`internal/grammars/scratch_test.go`, run it, read the output, then delete it:

```go
package grammars

import (
    "testing"
    sitter "github.com/tree-sitter/go-tree-sitter"
)

func TestScratch(t *testing.T) {
    src := []byte("function greet(name) return name end\n") // your language
    p := sitter.NewParser()
    defer p.Close()
    _ = p.SetLanguage(Lua())
    tree := p.Parse(src, nil)
    defer tree.Close()
    t.Log("\n" + tree.RootNode().ToSexp())
}
```

```bash
go test ./internal/grammars/ -run TestScratch -v
```

The s-expression names every node kind and field. You are looking for the kinds
that represent: function/method definitions, class-like definitions, call
sites, import statements, and the **field** that holds a declaration's name
(usually `name`). Cross-reference the upstream grammar's `node-types.json` if a
kind is ambiguous. Budget ~30 minutes per language.

### Step 3 — write the declaration

Create `internal/extract/langspec/<lang>.go`. Mirror the smallest existing
example, [`internal/extract/langspec/scala.go`](internal/extract/langspec/scala.go),
and fill in the kinds you found:

```go
package langspec

import (
    "github.com/luuuc/sense/internal/extract"
    "github.com/luuuc/sense/internal/grammars"
)

func init() {
    extract.Register(New(langSpec{
        Name:      "lua",
        Exts:      []string{".lua"},
        Grammar:   grammars.Lua(),
        Tier:      extract.TierStandard,
        Separator: ".",

        FuncTypes:   []string{"function_declaration"},
        ClassTypes:  []string{},               // Lua has no classes
        CallTypes:   []string{"function_call"},
        ImportTypes: []string{},               // require() is a call, not a stmt

        NameField: "name",
    }))
}
```

Every field of `langSpec` is documented inline in
[`internal/extract/langspec/langspec.go`](internal/extract/langspec/langspec.go) —
read those comments, they explain the *why* behind each. The fields you will
most likely set:

- **`FuncTypes` / `ClassTypes` / `CallTypes` / `ImportTypes`** — the node kinds
  from Step 2. Empty slices are fine for concepts the language lacks. **If your
  language expresses imports as ordinary function calls** (Lua `require("x")`,
  dynamic imports), leave `ImportTypes` empty — they will surface as `calls`
  edges; reach for the full tier only if you need a dedicated `imports` edge.
  **Even with `ImportTypes` set, the edge is silently dropped if the import
  node's path lives in a shape langspec doesn't recognize.** It reads the target
  from a fixed set of fields (`path`, `source`, `module_name`, a name field) and
  compound child kinds (`scoped_identifier`, `dotted_name`, …); a grammar that
  puts the path elsewhere (Haskell's `module:` field) yields no `imports` edge.
  Always confirm your import edge actually appears in the generated golden — if it
  doesn't, the import shape isn't covered, and a dedicated `imports` edge is a
  full-tier job.
- **`InheritFields`** — field names on a class node holding superclass/interface
  references (Java: `"superclass"`, `"interfaces"`). Use **`InheritKinds`**
  instead when the grammar exposes inheritance as a child node with no field
  name (C#: `"base_list"`).
- **`NameField`** — defaults to `"name"`; set it only if the grammar differs.
  **Caveat:** `langspec` reads the name node's text *verbatim*, with no
  per-segment splitting. If a grammar's name field is a compound node (Lua's
  `function Animal.new()` exposes `name` as a `dot_index_expression`), the symbol
  name and qualified name come out as the whole joined text (`Animal.new`), with
  no table/class parent and no `method` kind. That output is valid and queryable;
  if you need real scoping out of compound names, that is a full-tier extractor,
  not a langspec knob. Use `CallNameFn` for grammars whose *call* nodes lack a
  standard name field (Kotlin's fwcd grammar) — there is no equivalent hook for
  *symbol* names. Relatedly: a method defined under a node that is **not** in
  `ClassTypes` (a Haskell `instance`, an ML/Rust-style `impl` block) emits
  unscoped at top level, once per definition clause — expected for the standard
  tier; reach for the full tier if you need those bound to their type.
- **`VisibilityFn`** — optional. Most languages reuse the shared
  `accessModifierVisibility` helper behind a one-line wrapper; see the existing
  fns in [`internal/extract/langspec/visibility.go`](internal/extract/langspec/visibility.go)
  (`javaVisibility`, `scalaVisibility`, …). The default per language is the
  argument you pass (`"public"`, `"package"`, …). Leave nil if the grammar offers
  no access modifiers.
- **`AnnotationKinds`** — node kinds for annotations/attributes. Set these so an
  annotated-but-uncalled symbol (a `@Test`, a `[Fact]`) is kept open-world by the
  dead-code analyzer instead of being falsely reported dead.
- **`MentionKinds`** — opt-in. Only set these if the language has a real-world
  benchmark repo validating its `dead` tier; an empty list is the honest default
  (the language's symbols never earn `dead`). See the comment on the field, and
  the per-language soundness gate in the internal `.doc/definition/05-languages.md`
  (maintainer note above), before setting it.

### Step 4 — register the tier

Add one line to the `languageTiers` map in
[`internal/extract/extractor.go`](internal/extract/extractor.go):

```go
"lua": TierStandard,
```

This map is what `LanguageTier()` and `sense_status` report. It is **separate**
from the `Tier:` field in your declaration (that drives the extractor; this
drives reporting), and a language missing from the map silently reports as
`basic`. Both must agree.

### Step 5 — add fixtures and generate goldens

The test harness is discovery-driven: it walks `internal/extract/testdata/<lang>/`,
treats every file whose extension your extractor claims as a test case, and
compares the extractor's output against the companion `*.golden.json` (paired by
basename: `basic.lua` ↔ `basic.golden.json`). Adding a language needs **no new
test file** — only a fixture directory whose name matches the language.

1. Create a representative source fixture at
   `internal/extract/testdata/<lang>/basic.<ext>`. Make it small but exercise
   every concept your declaration claims: a couple of functions, a class with a
   parent if the language has classes, a call, an import. Look at
   [`internal/extract/testdata/java/basic.java`](internal/extract/testdata/java/basic.java)
   for the shape and scale.

2. Generate the golden:

   ```bash
   go test ./internal/extract -run 'TestFixtures/<lang>' -update
   ```

   (The scoped `-run` regenerates only your language; the bare `go test
   ./internal/extract -update` regenerates *every* golden — avoid it unless you
   mean to.) **Read the diff before committing it** — the golden is the contract,
   and `-update` will happily bless wrong output. Check that symbols have the
   right kinds, parents, and line ranges, and that inheritance/call edges appear
   where you expect. The runner sorts symbols by `(qualified, kind, line_start)`
   and edges by `(source, target, kind, line)`, so output is stable regardless
   of traversal order.

3. Run it for real (no `-update`) to confirm it passes:

   ```bash
   go test ./internal/extract -run 'TestFixtures/<lang>'
   ```

### Step 6 — verify the whole path

```bash
go test ./internal/grammars/ ./internal/extract/...
make ci
```

`make ci` enforces format, complexity, coverage, and the side-effect gates (see
[`CONTRIBUTING.md`](CONTRIBUTING.md)). A new langspec declaration is data, not
logic, so it adds no complexity and needs no new tests of its own beyond the
fixture — the shared `langspec` walker is already covered.

> If `go build ./...` or `make ci` fails on a missing `libonnxruntime` in the
> `embed` package, you skipped `scripts/fetch-deps.sh --local` from
> [Before you start](#before-you-start) — it is unrelated to your language. Your
> grammar and fixture tests (`go test ./internal/grammars/ ./internal/extract/...`)
> do not need it.

That is the whole standard-tier path: vendor, declare, register the tier,
fixture, done. No new blank-import, no new test file.

---

## Full tier (when langspec is not enough)

Reach for a bespoke extractor only when the standard tier genuinely cannot
express what you need: **type inference** (resolving a call's receiver type to
pick the right target), or **framework idioms** that create edges no syntactic
rule sees (Rails `has_many`, React JSX components, Django `@receiver`, Flask
routes). If your language is "just" classes, methods, calls, and imports, the
standard tier is the right and finished answer — do not build a package for it.

### The extractor contract

A full-tier extractor is its own package implementing the `extract.Extractor`
interface from
[`internal/extract/extractor.go`](internal/extract/extractor.go):

```go
type Extractor interface {
    Extract(tree *sitter.Tree, source []byte, filePath string, emit Emitter) error
    Grammar() *sitter.Language
    Language() string
    Extensions() []string // leading dot, lower-case: ".rb", ".ts"
    Tier() Tier
}
```

Emit through the callback `Emitter` (`Symbol(EmittedSymbol)` and
`Edge(EmittedEdge)`), both keyed by qualified name; the scan harness maps them to
IDs. Start from the Go extractor
([`internal/extract/golang/golang.go`](internal/extract/golang/golang.go)) as the
simplest complete example, or [`internal/extract/rust/`](internal/extract/rust/)
for a slightly larger one already split by concern.

**The `filePath` parameter.** The Go extractor ignores it (`_ string`), but
others use it: Ruby checks for `config/routes.rb`/`config/importmap.rb`, Python
detects `urls.py` for Django URL patterns. If your language has file-path-
dependent semantics, store `filePath` on the walker.

### The walker pattern

Every extractor follows the same shape — a private `walker` holding per-file
state, a recursive `walk` switching on `n.Kind()`, and a scope thread for nested
declarations:

```go
type walker struct {
    source []byte
    emit   extract.Emitter
    // language-specific fields
}

func (Extractor) Extract(tree *sitter.Tree, source []byte, filePath string, emit extract.Emitter) error {
    w := &walker{source: source, emit: emit}
    return w.walk(tree.RootNode(), nil)
}

func (w *walker) walk(n *sitter.Node, scope []string) error {
    switch n.Kind() {
    case "class_definition":
        return w.handleClass(n, scope)
    case "function_definition":
        return w.handleFunction(n, scope)
    default:
        return w.walkChildren(n, scope)
    }
}

func init() { extract.Register(Extractor{}) }
```

**Qualified names** are built from scope, with a language-idiomatic separator:
Ruby `Module::Class#method` (instance) / `.method` (class), Python
`module.Class.method`, Go `pkg.Type.Method`, Rust `module::Type::method`, TS/JS
`Class.method`.

**Body-walking for calls:** after emitting a symbol, walk its body with the
shared helper rather than re-rolling recursion:

```go
return extract.WalkNamedDescendants(body, "call_expression", func(c *sitter.Node) error {
    return w.emitCall(c, sourceQualified)
})
```

### What to extract, and what to skip

Extract named, addressable declarations: classes, functions, methods,
interfaces, constants, type definitions — the things a user would type
`sense graph "thing"` to ask about. Skip the ephemeral:

- **Lambdas / closures** — no stable name to query by.
- **Local variables** — internal to a function, not structural.
- **Generated code** — extract the declaration, not what a macro/source-generator
  expands to (tree-sitter can't see the expansion anyway).
- **Primitive and generic-parameter types** — don't emit `composes` for `int`,
  `string`, or the `T` in `Repository<T>`. Extract the class as `Repository`.

### Symbol kinds, edge kinds, confidence

| Symbol kind | Constant | Use for |
|---|---|---|
| `class` | `model.KindClass` | Classes, structs, records |
| `module` | `model.KindModule` | Modules, namespaces, packages |
| `method` | `model.KindMethod` | Instance/class methods, properties |
| `function` | `model.KindFunction` | Free functions |
| `constant` | `model.KindConstant` | Constants, const fields |
| `interface` | `model.KindInterface` | Interfaces, protocols, traits |
| `type` | `model.KindType` | Type aliases, enums, type definitions |

| Edge kind | Constant | Meaning |
|---|---|---|
| `calls` | `model.EdgeCalls` | A calls B |
| `imports` | `model.EdgeImports` | A imports B |
| `inherits` | `model.EdgeInherits` | A extends/implements B |
| `includes` | `model.EdgeIncludes` | A embeds/mixes in B |
| `tests` | `model.EdgeTests` | A is a test for B |
| `composes` | `model.EdgeComposes` | A has-a B (associations, typed fields) |

Use the shared confidence constants from `extract/extractor.go` — never invent
literals: `ConfidenceStatic` (1.0, a syntactic fact), `ConfidenceConvention`
(0.9, a framework naming pattern), `ConfidenceAmbiguous`/`ConfidenceTests` (0.8),
`ConfidenceDynamic` (0.7, dynamic dispatch on a literal arg). Lower
clamp/collision values exist for the resolver; the extractor side uses these.

**`composes` edges** apply to all extractors, not just frameworks: a property or
field with a user-defined type emits `composes`. Unwrap one level of generic
wrapper (`List<T>`, `Vec<T>`, `Optional<T>`) to the inner type; skip primitives
and stdlib types.

**Duplicate qualified names** (C# partial classes, Ruby reopened classes) are
expected: both files emit symbols under the same qualified name with different
`file_id`, and the resolver maps edges to both. Add a fixture if your language
allows it.

**Shared helpers** (use these, don't re-roll): `extract.Text(node, source)`
(nil-safe text), `extract.Line(point)` (0-indexed row → 1-indexed line),
`extract.WalkNamedDescendants(node, kind, visitor)` (depth-first body walk).

### Steps

1. **Vendor the grammar** — identical to standard-tier Step 1 (both pre-checks).
2. **Create the package** `internal/extract/<lang>/`, split by concern from the
   start. The existing extractors are the worked examples, each already
   decomposed: [`internal/extract/rust/`](internal/extract/rust/) is the smallest
   (`rust.go`, `traits.go`, `harvest.go`, `docstring.go`, `compose.go` —
   **read this first**); [`internal/extract/python/`](internal/extract/python/)
   shows framework inference in its own file; [`internal/extract/ruby/`](internal/extract/ruby/)
   and [`internal/extract/tsjs/`](internal/extract/tsjs/) are the deepest.
3. **Emit symbols and edges** through the `Emitter`, by qualified name, using the
   shared helpers and confidence constants above.
4. **Opt into dead-code soundness** if a framework reaches symbols with no source
   caller (decorators, FFI exports, test attributes). Implement the matching
   harvest emitter interface (`PythonHarvestEmitter`, `RustHarvestEmitter`,
   `TSHarvestEmitter`, `MentionEmitter` + `MentionHarvester`, …) so those symbols
   stay open-world. The interfaces and their reasons are documented inline in
   `extractor.go`; the per-language soundness gate is in `05-languages.md`.
5. **Register the tier** in `languageTiers` (standard-tier Step 4) as `TierFull`.
6. **Blank-import the package** in
   [`internal/extract/languages/languages.go`](internal/extract/languages/languages.go) —
   the line standard-tier languages skip. Without it the package's `init()` never
   runs and the extractor is invisible:

   ```go
   _ "github.com/luuuc/sense/internal/extract/lua"
   ```

7. **Fixtures and goldens** — identical to standard-tier Step 5; richer
   extractors warrant richer fixtures (one per framework idiom, plus negative
   cases).
8. **Update the definition doc** (maintainer step — the `.doc/` tree is internal,
   see the note at the top). Add a per-language entry to
   `.doc/definition/05-languages.md` following the existing format (extensions,
   symbol table, call-resolution rules, framework inference). That doc is the
   normative spec — keep it in sync with what the extractor actually does. An
   external contributor can skip this; a reviewer will handle it.
9. **Cover your logic.** Unlike a langspec declaration, a bespoke extractor is
   real logic held to the per-file coverage floor (see
   [`CONTRIBUTING.md`](CONTRIBUTING.md)). Unit-test the inference helpers
   directly, not only through goldens — the existing `*_test.go` files show the
   pattern.

---

## Adding framework support to an existing language

Adding a framework (Rails on Ruby, Django on Python, React on TypeScript) has its
own dedicated guide: [`CONTRIBUTING-A-FRAMEWORK.md`](CONTRIBUTING-A-FRAMEWORK.md).
It covers both halves of the job, emitting the framework's edges (associations,
routes, callbacks, cross-language wiring, the `RawExtractor` path) and the
dead-code fine-graining that keeps a framework's invisibly-reached symbols out of
the dead report. Start there rather than here once your language is full-tier.

---

## Reference: existing extractors

**Full tier (dedicated extractors):** Go (`golang/`, no framework, ~785 LOC);
Ruby (`ruby/`, Rails associations/callbacks/routes/Stimulus/Turbo, ~910);
Python (`python/`, Django + FastAPI, ~380 + `framework.go` ~550); TypeScript/JS
(`tsjs/`, React JSX, ~1085); Rust (`rust/`, none, ~860); ERB (`erb/`, Stimulus/
Turbo/Importmap cross-language, ~250).

**Standard tier (langSpec configs):** Java, Kotlin (custom `CallNameFn`), C#
(`InheritKinds` for `base_list`), C++ (`::` separator), C (functions/structs
only), PHP (`\` separator), Scala (field-based inheritance). Read the simplest
(`c.go`) first to learn the pattern, then `java.go` for a fuller example with
inheritance fields.

---

## When you get stuck

If a step here does not match what you see in the repo, the **doc** is wrong, not
you — open an issue or PR against this file. This guide is meant to be followable
end-to-end with zero gaps; a gap a newcomer hits is a bug in the guide.
