package dead

// coreVoice is the generic, language-agnostic voice. It raises a hand for
// the two ways ANY symbol can be reached invisibly regardless of language:
// it is a library's public API (an external consumer may exist), or its name
// is a reflection/metaprogramming dispatch target (it may be invoked
// dynamically). It never votes for `dead` — like every voice, it can only
// add caution.
//
// The core voice does NOT raise core_no_language_voice; that is the
// arbiter's fallback, because only the arbiter knows which language voices
// are registered.
type coreVoice struct{}

// Lang returns "" — the core voice applies to every language.
func (coreVoice) Lang() string { return "" }

// Inspect raises the reflection gate first (the more specific, more
// likely-live signal), then the export gate.
func (coreVoice) Inspect(s Symbol, f Facts) *Reason {
	// The dispatch set is keyed by language: a name is reflection-reachable only
	// if it appears as a dispatch target in its OWN language. Keying here in
	// lockstep with the arbiter's soundness gate keeps both per-language, so a
	// Ruby `send :foo` never keeps a Go `foo` open-world (or vice versa).
	if _, ok := f.DispatchNames[s.Language][s.Name]; ok {
		r := newReason(ReasonReflection)
		return &r
	}
	if f.IsLibrary && isPublicAPISymbol(s) {
		r := newReason(ReasonExportedAPI)
		return &r
	}
	return nil
}

// isPublicAPISymbol reports whether s is the kind of public symbol a library
// exposes as API. Visibility is the extractor's per-language notion of
// public (Go: exported/capitalized; Ruby: public once the visibility card
// lands). Constants and modules are excluded — a library's API surface is
// its callable/typed members.
func isPublicAPISymbol(s Symbol) bool {
	switch s.Kind {
	case "function", "method", "class", "type":
		return s.Visibility == "public"
	}
	return false
}
