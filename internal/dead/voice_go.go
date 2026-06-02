package dead

import "strings"

// goVoice is the Go language voice. Go is the first statically-typed, compiled
// language Sense reasons about, so its `dead` verdict can be made nearly
// airtight: visibility is structural (capitalized == exported), and an
// unexported symbol with no caller, no mention, and no invisible-reach idiom is
// exactly what `staticcheck U1000` flags as unused. The voice re-expresses Go's
// invisible-reach idioms as open-world reasons; like every voice it can only
// raise a hand (push → possibly_dead), never vote for `dead`.
//
// The earned-`dead` candidate is narrow and structural: an UNEXPORTED func /
// method / type with zero incoming edges, whose name is absent from the Go
// mention set (the arbiter's soundness gate) and which this voice cannot tie to
// init / interface satisfaction / cgo / generated code. Everything else raises a
// hand. In particular every EXPORTED symbol stays open-world — `staticcheck
// U1000` only flags unexported symbols, so letting an exported one earn `dead`
// would break the binding "Sense `dead` ⊆ U1000" gate (an exported symbol may be
// used by another package Sense never indexed).
type goVoice struct{}

func (goVoice) Lang() string { return "go" }

// Inspect returns the most-specific (most-likely-live) reason a hidden caller
// could exist for s, or nil when s is an unexported func/method/type with no
// invisible-reach idiom — the only shape that may fall through to `dead`. Checks
// are ordered most-live-first so the returned reason carries the most useful
// hint; the arbiter independently picks the lowest-priority reason across voices.
func (goVoice) Inspect(s Symbol, f Facts) *Reason {
	// init() is run by the Go runtime at package load, never by a caller. (It is
	// also filtered upstream as an entry point; this is defense in depth so the
	// voice's contract holds regardless of the candidate pipeline.)
	if isGoInit(s) {
		return reasonPtr(ReasonGoInit)
	}
	// A method satisfying an interface is reachable through any implementor, where
	// the static graph shows zero direct callers. Go satisfaction is structural,
	// so name match against indexed interface methods (plus a stdlib magic-method
	// table) is the soundest signal without recomputing satisfaction.
	if isGoInterfaceMethod(s, f) {
		return reasonPtr(ReasonGoInterface)
	}
	// A cgo `//export`ed function is called from C; no Go caller edge exists.
	if _, ok := f.CgoExportNames[s.Name]; ok {
		return reasonPtr(ReasonGoCgo)
	}
	// A symbol in a generated file is produced by a tool; it should be regenerated
	// from its source, not hand-deleted, and may be referenced only by other
	// generated code outside this scan's view.
	if isGoGeneratedFile(s.File) {
		return reasonPtr(ReasonGoGenerated)
	}
	// Exported symbols never earn `dead`: U1000 flags only unexported symbols, so
	// an exported one may have an external consumer Sense never indexed. A library
	// is handled by the core voice (core_exported_api); otherwise raise go_exported.
	if s.Visibility == "public" {
		if f.IsLibrary && isPublicAPISymbol(s) {
			return nil
		}
		return reasonPtr(ReasonGoExported)
	}
	// Unexported from here. Constants and package-level variables (both KindConstant
	// in the Go extractor) stay open-world: an unused iota anchor is load-bearing
	// for its sequence yet looks unreferenced, and a var may be mutated/read through
	// a path the resolver cannot bind. Only func/method/type may fall through.
	switch s.Kind {
	case "function", "method", "type", "class", "interface":
		return nil
	default:
		return reasonPtr(ReasonGoConst)
	}
}

// isGoInit reports whether s is a package init function (`func init()`).
func isGoInit(s Symbol) bool {
	return s.Kind == "function" && s.Name == "init"
}

// isGoInterfaceMethod reports whether s is a method whose name is declared on an
// interface in this index, or is a well-known stdlib interface method. Either
// way the method may be invoked through the interface, where the static graph
// shows no direct caller, so it must stay open-world.
func isGoInterfaceMethod(s Symbol, f Facts) bool {
	if s.Kind != "method" {
		return false
	}
	if _, ok := f.InterfaceMethodNames[s.Name]; ok {
		return true
	}
	_, magic := goMagicMethods[s.Name]
	return magic
}

// goMagicMethods are method names the Go runtime / standard library invokes
// through interfaces that are NOT in Sense's index (fmt.Stringer, error,
// json.Marshaler, io.Reader/Writer/Closer, sort.Interface, …). A method with one
// of these names is reached by the stdlib, never by an indexed caller, so it
// stays open-world. Signature is not checked — a name match over-approximates
// toward caution (recall loss at worst), which is the safe direction.
//
// Every name here is exported (stdlib interfaces have exported methods), and only
// UNEXPORTED methods can earn `dead`, so this table never changes a verdict — an
// exported method is already `possibly_dead`. Its job is hint accuracy: it upgrades
// such a method's reason from go_exported to the more precise go_interface.
var goMagicMethods = map[string]struct{}{
	"String": {}, "GoString": {}, "Error": {}, "Format": {},
	"MarshalJSON": {}, "UnmarshalJSON": {},
	"MarshalText": {}, "UnmarshalText": {},
	"MarshalBinary": {}, "UnmarshalBinary": {},
	"Read": {}, "Write": {}, "Close": {},
	"Scan": {}, "Value": {},
	"Len": {}, "Less": {}, "Swap": {},
	"Unwrap": {}, "Is": {}, "As": {},
	"ServeHTTP": {},
}

// goGeneratedSuffixes name files emitted by code generators. A symbol in such a
// file should be regenerated, not hand-deleted. Filename-based detection is a
// conservative subset of the canonical `// Code generated … DO NOT EDIT.` header
// (which needs file content the voice does not carry); it covers the common
// tools (protoc → *.pb.go, `go:generate` → *_gen.go / *_generated.go).
var goGeneratedSuffixes = []string{".pb.go", "_gen.go", "_generated.go"}

// isGoGeneratedFile reports whether path looks generator-produced.
func isGoGeneratedFile(path string) bool {
	for _, suf := range goGeneratedSuffixes {
		if strings.HasSuffix(path, suf) {
			return true
		}
	}
	// k8s-style `zz_generated.*.go`.
	base := path
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	return strings.HasPrefix(base, "zz_generated")
}

func init() {
	registerReasons(map[string]reasonSpec{
		ReasonGoInit: {
			priority: 20,
			hint:     "Go init() is run by the runtime at package load, not by a caller; never remove it as dead",
			verify:   "`func init()` is invoked by the Go runtime when the package loads. It has no caller by design; do not remove it.",
		},
		ReasonGoInterface: {
			priority: 30,
			hint:     "method satisfies an interface (named on an interface, or a stdlib interface like fmt.Stringer/error/json.Marshaler); it may be called through the interface — confirm no implementor is used before removing",
			verify:   "This method shares a name with an interface method, so it may be invoked through the interface (where Sense sees no direct caller). Check whether the receiver type is stored in an interface variable anywhere before removing.",
		},
		ReasonGoCgo: {
			priority: 30,
			hint:     "function is cgo `//export`ed and called from C; no Go caller exists — remove only if the C side no longer uses it",
			verify:   "This function is exported to C via a cgo `//export` directive and has no Go caller by design. Check the C code that calls it before removing.",
		},
		ReasonGoGenerated: {
			priority: 40,
			hint:     "symbol lives in a generated file (*.pb.go / *_gen.go / *_generated.go); regenerate from its source rather than hand-deleting",
			verify:   "This symbol is in a generated file. Change the generator input and regenerate rather than editing it directly; it may also be referenced by other generated code outside this scan.",
		},
		ReasonGoConst: {
			priority: 45,
			hint:     "Go constant or package-level variable; it may anchor an iota sequence or be referenced through a path Sense cannot bind — confirm before removing",
			verify:   "Go constants/vars can be load-bearing iota anchors or referenced indirectly. For each, grep for its name across the package (including iota groups and struct tags) before removing.",
		},
		ReasonGoExported: {
			priority: 50,
			hint:     "exported Go symbol with no caller in this repo; another package may use it (staticcheck flags only unexported symbols) — search dependents before removing",
			verify:   "Exported Go symbols can be used by packages outside this repo. For each, search dependent modules and the rest of this tree for the qualified name before removing.",
		},
	})
}
