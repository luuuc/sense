package dead

import "testing"

func goSym(name, qualified, kind, visibility, file string) Symbol {
	return Symbol{
		Name:       name,
		Qualified:  qualified,
		Kind:       kind,
		Language:   "go",
		Visibility: visibility,
		File:       file,
	}
}

func TestGoVoiceInit(t *testing.T) {
	v := goVoice{}
	// init() is runtime-invoked: always raises go_init, never earns dead.
	assertReason(t, v, goSym("init", "pkg.init", "function", "private", "pkg.go"), Facts{}, ReasonGoInit)
	// A capitalized Init is an ordinary function, not the runtime initializer.
	assertReason(t, v, goSym("Init", "pkg.Init", "function", "public", "pkg.go"), Facts{IsLibrary: false}, ReasonGoExported)
}

func TestGoVoiceInterfaceByDeclaredName(t *testing.T) {
	v := goVoice{}
	f := Facts{InterfaceMethodNames: map[string]struct{}{"Handle": {}}}
	// A method whose name is declared on some interface stays open-world.
	assertReason(t, v, goSym("Handle", "pkg.T.Handle", "method", "private", "t.go"), f, ReasonGoInterface)
	// A package function of the same name is NOT an interface method.
	assertReason(t, v, goSym("Handle", "pkg.Handle", "function", "private", "t.go"), f, "")
}

func TestGoVoiceInterfaceByMagicMethod(t *testing.T) {
	v := goVoice{}
	// Stdlib interface methods (not in the index) are recognised by name.
	for _, name := range []string{"String", "Error", "MarshalJSON", "ServeHTTP", "Read"} {
		assertReason(t, v, goSym(name, "pkg.T."+name, "method", "private", "t.go"), Facts{}, ReasonGoInterface)
	}
	// A non-magic unexported method falls through to silent.
	assertReason(t, v, goSym("frobnicate", "pkg.T.frobnicate", "method", "private", "t.go"), Facts{}, "")
}

func TestGoVoiceCgo(t *testing.T) {
	v := goVoice{}
	f := Facts{CgoExportNames: map[string]struct{}{"goCallback": {}}}
	assertReason(t, v, goSym("goCallback", "pkg.goCallback", "function", "private", "cgo.go"), f, ReasonGoCgo)
	// A function not in the cgo set is unaffected.
	assertReason(t, v, goSym("plain", "pkg.plain", "function", "private", "cgo.go"), f, "")
}

func TestGoVoiceGenerated(t *testing.T) {
	v := goVoice{}
	for _, file := range []string{"api.pb.go", "schema_gen.go", "model_generated.go", "k8s/zz_generated.deepcopy.go"} {
		assertReason(t, v, goSym("helper", "pkg.helper", "function", "private", file), Facts{}, ReasonGoGenerated)
	}
	// A hand-written file is not generated.
	assertReason(t, v, goSym("helper", "pkg.helper", "function", "private", "service.go"), Facts{}, "")
}

func TestGoVoiceExported(t *testing.T) {
	v := goVoice{}
	// Exported symbol in a binary (not a library) → go_exported, never dead.
	assertReason(t, v, goSym("Handler", "pkg.Handler", "function", "public", "h.go"), Facts{IsLibrary: false}, ReasonGoExported)
	assertReason(t, v, goSym("Config", "pkg.Config", "class", "public", "c.go"), Facts{IsLibrary: false}, ReasonGoExported)
	// Exported callable/type in a LIBRARY → silent here; the core voice raises
	// core_exported_api instead (proven by the arbiter-level test below).
	assertReason(t, v, goSym("PublicFn", "pkg.PublicFn", "function", "public", "l.go"), Facts{IsLibrary: true}, "")
	// An exported CONSTANT in a library is not a callable/type API surface, so the
	// core voice would not claim it; the Go voice keeps it open-world (go_exported).
	assertReason(t, v, goSym("MaxSize", "pkg.MaxSize", "constant", "public", "l.go"), Facts{IsLibrary: true}, ReasonGoExported)
}

func TestGoVoiceUnexportedConst(t *testing.T) {
	v := goVoice{}
	// Unexported const/var (KindConstant) stays open-world: iota-anchor / dynamic
	// reference risk means it must not earn dead.
	assertReason(t, v, goSym("threshold", "pkg.threshold", "constant", "private", "c.go"), Facts{}, ReasonGoConst)
}

func TestGoVoiceSilentUnexportedFuncMethodType(t *testing.T) {
	v := goVoice{}
	// The only shapes that may fall through to dead: unexported func/method/type/
	// class/interface with no invisible-reach idiom.
	assertReason(t, v, goSym("helper", "pkg.helper", "function", "private", "s.go"), Facts{}, "")
	assertReason(t, v, goSym("compute", "pkg.T.compute", "method", "private", "s.go"), Facts{}, "")
	assertReason(t, v, goSym("widget", "pkg.widget", "type", "private", "s.go"), Facts{}, "")
	assertReason(t, v, goSym("box", "pkg.box", "class", "private", "s.go"), Facts{}, "")
	assertReason(t, v, goSym("reader", "pkg.reader", "interface", "private", "s.go"), Facts{}, "")
}

func TestGoVoiceLang(t *testing.T) {
	if (goVoice{}).Lang() != "go" {
		t.Errorf("goVoice.Lang() = %q, want go", (goVoice{}).Lang())
	}
}

// TestGoVoiceReasonPriorityOrder pins the most-live-first ordering: when a symbol
// matches several signals, the more-likely-live reason is returned.
func TestGoVoiceReasonPriorityOrder(t *testing.T) {
	v := goVoice{}
	// init in a generated file → go_init wins over go_generated.
	assertReason(t, v, goSym("init", "pkg.init", "function", "private", "x_gen.go"), Facts{}, ReasonGoInit)
	// An exported method matching an interface name → go_interface wins over go_exported.
	f := Facts{InterfaceMethodNames: map[string]struct{}{"Handle": {}}}
	assertReason(t, v, goSym("Handle", "pkg.T.Handle", "method", "public", "t.go"), f, ReasonGoInterface)
}
