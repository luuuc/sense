# Adding a Language or Framework

How to add a new language extractor or framework support to Sense.

## Architecture overview

Every file Sense indexes flows through the same pipeline:

1. **Walk** -- the scan harness discovers files and matches each extension to a registered extractor via `extract.ForExtension()`. Files with no matching extractor are skipped entirely.
2. **Parse** -- tree-sitter parses the source into a concrete syntax tree (CST).
3. **Extract** -- the language extractor walks the CST and emits symbols and edges through a callback interface.
4. **Resolve** -- a cross-file pass maps qualified-name edges to numeric symbol IDs.
5. **Post-passes** -- interface satisfaction, test association, embeddings.

Extractors are stateless and per-file. They never read other files or query the database. Cross-file relationships are the resolver's job.

## Adding a new language

Seven steps, in order.

### 1. Bundle the tree-sitter grammar

```bash
go get github.com/tree-sitter/tree-sitter-<lang>
```

Create `internal/grammars/<lang>.go`:

```go
package grammars

import (
    sitter "github.com/tree-sitter/go-tree-sitter"
    ts_lang "github.com/tree-sitter/tree-sitter-<lang>/bindings/go"
)

func Lang() *sitter.Language { return sitter.NewLanguage(ts_lang.Language()) }
```

The grammar compiles into the binary via CGo at `go build` time. Verify it links:

```bash
go test ./internal/grammars/
```

**Grammar gate:** Before writing any extractor code, verify the grammar parses modern syntax for your language. Write a small test file using recent language features (e.g. C# 10 file-scoped namespaces, Python 3.12 type parameter syntax) and confirm tree-sitter produces a valid CST without `ERROR` nodes. If the grammar chokes on modern syntax, stop — fixing upstream grammars will blow your time budget.

### 2. Write the extractor

Create `internal/extract/<lang>/<lang>.go`. The extractor must implement:

```go
type Extractor interface {
    Extract(tree *sitter.Tree, source []byte, filePath string, emit Emitter) error
    Grammar() *sitter.Language
    Language() string
    Extensions() []string // leading dot, lower-case: ".rb", ".ts"
    Tier() Tier
}
```

The `Emitter` interface has two methods:

```go
type Emitter interface {
    Symbol(EmittedSymbol) error
    Edge(EmittedEdge) error
}
```

Start from the Go extractor (`internal/extract/golang/golang.go`) as the simplest complete example.

**The `filePath` parameter:** The Go extractor ignores it (`_ string`), but other extractors use it. Ruby checks for `config/routes.rb` and `config/importmap.rb` to trigger route/importmap parsing. Python uses it to detect `urls.py` for Django URL pattern extraction. If your language has file-path-dependent semantics (e.g. C# file-scoped namespaces, top-level statements), store `filePath` on the walker.

The pattern every extractor follows:

**Struct:** A private `walker` holds per-file state:

```go
type walker struct {
    source []byte
    emit   extract.Emitter
    // language-specific fields
}
```

**Entry point:** `Extract` creates a walker and calls its top-level walk:

```go
func (Extractor) Extract(tree *sitter.Tree, source []byte, filePath string, emit extract.Emitter) error {
    w := &walker{source: source, emit: emit}
    return w.walk(tree.RootNode(), nil)
}
```

**Recursive walk:** Switch on `n.Kind()` to dispatch to handler methods. Thread a `scope []string` for languages with nesting (classes, modules):

```go
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
```

**Qualified names:** Build from scope. The separator is language-idiomatic:
- Ruby: `Module::Class#method` (instance), `Module::Class.method` (class)
- Python: `module.Class.method`
- Go: `pkg.Type.Method`
- Rust: `module::Type::method`
- TS/JS: `Class.method`

**Body walking for calls:** After emitting a symbol, walk the body for call expressions using the shared helper:

```go
return extract.WalkNamedDescendants(body, "call_expression", func(c *sitter.Node) error {
    return w.emitCall(c, sourceQualified)
})
```

**Registration:** Every extractor registers itself at init:

```go
func init() { extract.Register(Extractor{}) }
```

### What to extract and what to skip

Extract named, addressable declarations: classes, functions, methods, interfaces, constants, type definitions. These are the symbols users ask about ("who calls this?", "what implements this?").

Skip anonymous or ephemeral constructs:
- **Lambdas and closures** -- too granular, no stable name to query by.
- **Local variables** -- internal to a function, not part of the structural graph.
- **Generated code** -- if a language has code generation (C# source generators, Rust proc macros), the generator output is invisible to tree-sitter. Extract the declaration, not what it expands to.
- **Primitive types** -- don't emit `composes` edges for `int`, `string`, `bool`, etc. Only emit for user-defined types.
- **Generic type parameters** -- `T` in `class Repository<T>` is a compile-time constraint, not a structural relationship. Extract the class as `Repository`, ignore `T`.

When in doubt: if a user would never type `sense graph "thing"` to ask about it, don't extract it.

### Typed fields and `composes` edges

Properties, fields, and parameters with user-defined types should emit `composes` edges. This applies to all extractors, not just frameworks:

```csharp
class Order {
    public User Customer { get; set; }      // → composes → User
    public List<OrderItem> Items { get; set; } // → composes → OrderItem (unwrap generic)
    public int Quantity { get; set; }        // skip: primitive
}
```

Unwrap one level of generic wrappers (`List<T>`, `Optional<T>`, `Vec<T>`, `Box<T>`) to extract the inner type. Skip language primitives and standard library types.

### Duplicate qualified names (partial classes, reopened classes)

Some languages allow the same symbol to be defined across multiple files -- C# partial classes, Ruby reopened classes. Both files emit symbols under the same qualified name with different `file_id` values. The resolver maps edges to both via the shared qualified name. Write a fixture that tests this if your language supports it.

### Available symbol kinds

| Kind | Constant | Use for |
|---|---|---|
| `class` | `model.KindClass` | Classes, structs, records |
| `module` | `model.KindModule` | Modules, namespaces, packages |
| `method` | `model.KindMethod` | Instance/class methods, properties |
| `function` | `model.KindFunction` | Free functions |
| `constant` | `model.KindConstant` | Constants, const fields |
| `interface` | `model.KindInterface` | Interfaces, protocols, traits |
| `type` | `model.KindType` | Type aliases, enums, type definitions |

### Available edge kinds

| Kind | Constant | Meaning |
|---|---|---|
| `calls` | `model.EdgeCalls` | A calls B |
| `imports` | `model.EdgeImports` | A imports B |
| `inherits` | `model.EdgeInherits` | A extends/implements B |
| `includes` | `model.EdgeIncludes` | A embeds/mixes in B |
| `tests` | `model.EdgeTests` | A is a test for B |
| `composes` | `model.EdgeComposes` | A has-a B (associations, typed fields) |

### Confidence levels

Use the shared constants from `extract/extractor.go`:

| Constant | Value | When to use |
|---|---|---|
| `ConfidenceStatic` | 1.0 | Direct syntactic fact (a call expression, a superclass clause) |
| `ConfidenceConvention` | 0.9 | Framework convention (Rails `has_many`, Django field) |
| `ConfidenceAmbiguous` | 0.8 | Multiple candidates, or unqualified fallback |
| `ConfidenceTests` | 0.8 | Test-to-implementation match by filename convention |
| `ConfidenceDynamic` | 0.7 | Dynamic dispatch with a literal argument (`send(:name)`, `getattr(obj, "name")`) |

### Shared helpers

Use these from the `extract` package instead of writing your own:

- `extract.Text(node, source)` -- nil-safe node text. Returns `""` for nil nodes.
- `extract.Line(point)` -- converts tree-sitter's 0-indexed row to 1-indexed line number.
- `extract.WalkNamedDescendants(node, kind, visitor)` -- depth-first walk of named descendants matching a CST node kind. Use this for body-walking (calls, assignments, etc.).

### 3. Register the extractor

Add a blank import to `internal/extract/languages/languages.go`:

```go
import (
    // ...existing imports...
    _ "github.com/luuuc/sense/internal/extract/<lang>"
)
```

### 4. Add the tier

Add an entry to the `languageTiers` map in `internal/extract/extractor.go`:

```go
var languageTiers = map[string]Tier{
    // ...existing entries...
    "<lang>": TierStandard,
}
```

### 5. Write test fixtures

Create `internal/extract/testdata/<lang>/` with at least one source file. The fixture harness discovers files automatically by matching the extractor's claimed extensions.

For each source file `foo.<ext>`, a golden file `foo.golden.json` holds the expected output:

```json
{
  "symbols": [
    {
      "name": "MyClass",
      "qualified": "MyClass",
      "kind": "class",
      "visibility": "public",
      "line_start": 1,
      "line_end": 10
    }
  ],
  "edges": [
    {
      "source": "MyClass.process",
      "target": "helper",
      "kind": "calls",
      "line": 5,
      "confidence": 1
    }
  ]
}
```

Generate goldens from your extractor output:

```bash
go test ./internal/extract -update
```

Review the generated JSON before committing. The fixture runner sorts symbols by `(qualified, kind, line_start)` and edges by `(source, target, kind, line)`, so output is stable regardless of traversal order.

### 6. Update the language definition doc

Add a section for your language in `.doc/definition/05-languages.md`. Follow the format of existing entries (Ruby, Python, Go, etc.): file extensions, symbol extraction table, call resolution rules, and any framework inference. This doc is the normative spec -- keep it in sync with what your extractor actually does.

### 7. Verify

```bash
make ci
```

This builds, runs all tests (including your new fixtures), and lints.

## Adding framework support to an existing language

Framework inference adds domain-specific edges on top of a language's base extraction. Two patterns exist in the codebase:

### Pattern A: Separate file (recommended for non-trivial frameworks)

Python uses this: `internal/extract/python/framework.go` contains all Django and FastAPI logic as methods on the same `walker` struct from `python.go`.

The base extractor calls into framework methods at integration points:

```go
// In python.go's handleAssignment:
if err := w.emitDjangoModelField(assign, scope); err != nil {
    return err
}
```

Framework methods check whether the code matches a framework pattern and emit additional edges. When there's no match, they return nil and the base extraction continues unchanged.

### Pattern B: Inline (for lightweight framework support)

Ruby uses this: Rails-specific extraction lives directly in `ruby.go`. The `dispatchCall` method acts as a router, checking method names against known patterns:

```go
case "has_many", "belongs_to", "has_one":
    return false, w.emitAssociationEdge(n, scope, methodName)
```

Use this pattern when framework support is a handful of `case` branches. Move to a separate file when it grows.

### What framework extractors emit

Framework inference typically emits these edge kinds:

- **`composes`** -- ORM associations and typed fields. Rails `has_many :orders` emits a `composes` edge from the model to `Order`. Django `ForeignKey(User)` does the same.
- **`calls`** -- Lifecycle callbacks and route-to-handler wiring. Rails `before_action :authenticate` emits a `calls` edge to the `authenticate` method. FastAPI `@app.get("/users")` connects the route to the handler.
- **`imports`** -- Module/package inclusion. Rails importmap `pin` declarations emit `imports` edges to JS files.

Framework edges use `ConfidenceConvention` (0.9) since they rely on naming patterns, not syntactic proof.

### Cross-language edges

Some frameworks bridge languages. The ERB extractor (`internal/extract/erb/`) connects templates to JavaScript:

- `data-controller="checkout"` emits an edge targeting `CheckoutController` in JS
- `<turbo-stream-from>` emits edges using the `turbo-channel:` prefix

Cross-language resolution works through synthetic qualified-name prefixes defined in `extract/extractor.go`:

```go
const (
    PrefixTurboChannel = "turbo-channel:"
    PrefixTurboFrame   = "turbo-frame:"
    PrefixImportmap    = "importmap:"
)
```

Both sides (emitter and receiver) must produce the same qualified name. The shared helper `extract.StimulusControllerQualified()` ensures ERB and TS/JS extractors agree on Stimulus controller names.

### RawExtractor (for non-tree-sitter languages)

Template languages like ERB don't have tree-sitter grammars. Implement the `RawExtractor` interface instead:

```go
type RawExtractor interface {
    ExtractRaw(source []byte, filePath string, emit Emitter) error
}
```

When an extractor implements both `Extractor` and `RawExtractor`, the scan harness calls `ExtractRaw` and skips tree-sitter parsing. `Grammar()` should return `nil`. See `internal/extract/erb/` for the reference implementation.

### Test fixtures for framework support

Add framework-specific fixtures alongside the language's existing ones in `internal/extract/testdata/<lang>/`. Name them descriptively:

```
testdata/ruby/rails_associations.rb
testdata/ruby/rails_associations.golden.json
testdata/python/django_models.py
testdata/python/django_models.golden.json
```

Include negative cases: patterns that look like framework code but shouldn't emit edges.

## Reference: existing extractors

| Language | Directory | Framework support | Lines |
|---|---|---|---|
| Go | `internal/extract/golang/` | None | ~785 |
| Ruby | `internal/extract/ruby/` | Rails (associations, callbacks, routes, Stimulus, Turbo) | ~910 + inflect.go (~50) |
| Python | `internal/extract/python/` | Django (models, URLs), FastAPI (routes, Depends) | ~380 + framework.go (~550) |
| TypeScript/JS | `internal/extract/tsjs/` | React (JSX component calls) | ~1085 |
| Rust | `internal/extract/rust/` | None | ~860 |
| ERB | `internal/extract/erb/` | Stimulus, Turbo, Importmap (cross-language) | ~250 |

Read the simplest one first (Go for no-framework, Python for framework support) before starting yours.
