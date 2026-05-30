package dead

// rubyVoice is the first ported language voice. It re-expresses the Ruby
// knowledge the old subtract-cascade encoded as open-world reasons: a Ruby
// symbol that looks unreferenced may still be reached through duck-typed
// dispatch, autoloading, const_get, value-object surfaces, service-object
// call conventions, or module mixins. Like every voice it can only raise a
// hand (push → possibly_dead); it never votes for dead.
//
// The governing rule (pitch 25-13 decision #2): only a private/protected Ruby
// method that this voice cannot tie to any dynamic-reach idiom may fall
// through to `dead`. Every public method, and every class/module/constant,
// raises a hand — because a public method can be invoked on a receiver whose
// type the static indexer never resolves, and a constant can be reached by
// const_get / autoload / STI. So no Ruby class, module, constant, or public
// method ever earns `dead`; the earned-dead set is exactly the non-special
// private methods.
type rubyVoice struct{}

func (rubyVoice) Lang() string { return "ruby" }

func (rubyVoice) Inspect(s Symbol, f Facts) *Reason {
	switch s.Kind {
	case "class":
		return reasonPtr(ReasonRubyClass)
	case "module":
		return reasonPtr(ReasonRubyModule)
	case "constant":
		return reasonPtr(ReasonRubyConstant)
	case "method":
		return rubyMethodReason(s, f)
	}
	return nil
}

// rubyMethodReason classifies a zero-reference Ruby method. The order is
// most-specific-first so the reason carries the most useful hint; the arbiter
// independently picks the lowest-priority (most-likely-live) reason when
// several voices raise hands, but within this one voice we return the single
// best-fitting reason.
func rubyMethodReason(s Symbol, f Facts) *Reason {
	// A value object's instance methods form a duck-typed API surface
	// (`result.success?` on a local whose type the indexer cannot infer).
	if isValueObjectMethod(s, f.ValueObjectClassIDs) {
		return reasonPtr(ReasonRubyValueObject)
	}
	// A service/command object's `call` is invoked via Klass.new.call / .() —
	// a duck-typed entry point the static graph rarely ties back.
	if isServiceCall(s) {
		return reasonPtr(ReasonRubyServiceCall)
	}
	// A method on a module included somewhere is reachable through the
	// including type, which the per-method graph does not capture.
	if s.ParentID != nil {
		if _, ok := f.IncludedModuleIDs[*s.ParentID]; ok {
			return reasonPtr(ReasonRubyModuleMixin)
		}
	}
	// The catch-all: any public (or visibility-unknown) instance/singleton
	// method can be reached by duck-typed dispatch on an unresolved receiver.
	// This is the rule that keeps every public Ruby method possibly_dead.
	if s.Visibility == "public" || s.Visibility == "" {
		return reasonPtr(ReasonRubyPublicMethod)
	}
	// A private/protected method with no special reach: the voice stays
	// silent, so a registered-language symbol with no other raised hand can
	// earn `dead` in the arbiter.
	return nil
}

// isServiceCall reports whether s is the `call` entry point of a service /
// command object (Klass.new.call / Klass.() conventions). Re-expresses the
// old isDynamicServiceCall as a voice predicate so the cascade copy can be
// deleted; it reuses the surviving serviceClassSuffixes + helpers.
func isServiceCall(s Symbol) bool {
	if s.Name != "call" {
		return false
	}
	return hasAnySuffix(rubyMethodParentName(s.Qualified), serviceClassSuffixes)
}

// reasonPtr builds a heap Reason from a catalog code for returning as *Reason.
func reasonPtr(code string) *Reason {
	r := newReason(code)
	return &r
}

func init() {
	registerReasons(map[string]reasonSpec{
		ReasonRubyValueObject: {
			priority: 25,
			hint:     "value-object instance method reached via x.method on a duck-typed local; grep for .name before removing",
			verify:   "Value-object (Struct/Data) instance methods are called on locals whose type Sense can't infer. For each, grep for `.name` across the repo before removing.",
		},
		ReasonRubyServiceCall: {
			priority: 25,
			hint:     "service-object entry point invoked via Klass.new.call or .(); confirm no caller constructs and calls it before removing",
			verify:   "Service/command objects are invoked via `Klass.new.call` or `Klass.()`. For each, grep for the class name being constructed and called before removing.",
		},
		ReasonRubyModuleMixin: {
			priority: 35,
			hint:     "method defined on a module that is included elsewhere; it is an instance method of every includer — check includers before removing",
			verify:   "These methods live on a mixed-in module. Find `include <Module>` sites and grep those classes' callers for `.name` before removing.",
		},
		ReasonRubyPublicMethod: {
			priority: 60,
			hint:     "public Ruby method with no static caller; may be reached by duck-typed dispatch — grep for .name before removing",
			verify:   "Public Ruby methods can be called on a receiver whose type Sense can't resolve. For each, grep for `.name` (and the bare name in views/serializers) before removing.",
		},
		ReasonRubyClass: {
			priority: 55,
			hint:     "Ruby class with no static reference; may be reached via autoload, const_get, or STI — confirm before removing",
			verify:   "Ruby classes can be loaded by autoload / const_get / STI type columns. For each, grep for the constant name as a string and bare reference before removing.",
		},
		ReasonRubyModule: {
			priority: 55,
			hint:     "Ruby module with no static reference; may be included or resolved by name — confirm before removing",
			verify:   "Ruby modules can be included or resolved by name. For each, grep for the constant name before removing.",
		},
		ReasonRubyConstant: {
			priority: 55,
			hint:     "Ruby constant with no static reference; may be resolved via const_get / constantize — confirm before removing",
			verify:   "Ruby constants can be resolved dynamically (const_get/constantize). For each, grep for the name as a string and bare reference before removing.",
		},
	})
}
