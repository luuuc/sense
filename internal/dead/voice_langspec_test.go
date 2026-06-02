package dead

import "testing"

func lsSym(name, kind, lang, visibility string) Symbol {
	return Symbol{
		Name:       name,
		Qualified:  name,
		Kind:       kind,
		Language:   lang,
		Visibility: visibility,
		File:       "Thing." + lang,
	}
}

func TestLangspecVoiceLang(t *testing.T) {
	for _, lang := range []string{"java", "kotlin", "csharp", "scala", "cpp", "php", "c"} {
		if got := (langspecVoice{lang: lang}).Lang(); got != lang {
			t.Errorf("langspecVoice{%q}.Lang() = %q", lang, got)
		}
	}
}

func TestLangspecVoiceAnnotatedWins(t *testing.T) {
	v := langspecVoice{lang: "java"}
	f := Facts{LangspecAnnotatedNames: nameSet("handler")}
	// Annotated beats every other gate, even for a private method that would
	// otherwise fall silent in a dead-eligible language.
	assertReason(t, v, lsSym("handler", "method", "java", "private"), f, ReasonLangspecAnnotated)
	assertReason(t, v, lsSym("handler", "method", "java", "public"), f, ReasonLangspecAnnotated)
}

func TestLangspecVoiceInterfaceMethod(t *testing.T) {
	v := langspecVoice{lang: "java"}
	f := Facts{InterfaceMethodNames: nameSet("execute")}
	// A method whose name matches an interface method is reachable through any
	// implementor — even when explicitly private.
	assertReason(t, v, lsSym("execute", "method", "java", "private"), f, ReasonLangspecInterfaceMethod)
	// The interface gate is method-only: a same-named field/constant is not it.
	assertReason(t, v, lsSym("execute", "constant", "java", "public"), f, ReasonLangspecPublicNoFramework)
}

func TestLangspecVoicePublicNoFramework(t *testing.T) {
	v := langspecVoice{lang: "java"}
	// A public application symbol stays open-world: a framework Sense does not
	// model may dispatch it.
	assertReason(t, v, lsSym("Service", "class", "java", "public"), Facts{}, ReasonLangspecPublicNoFramework)
	assertReason(t, v, lsSym("run", "method", "java", "public"), Facts{}, ReasonLangspecPublicNoFramework)
	// Visibility-unknown ("") is treated as public — the safe direction.
	assertReason(t, v, lsSym("legacy", "method", "java", ""), Facts{}, ReasonLangspecPublicNoFramework)
}

func TestLangspecVoiceLibraryPublicDefersToCoreVoice(t *testing.T) {
	v := langspecVoice{lang: "java"}
	f := Facts{IsLibrary: true}
	// A library's public callable/type API is the core voice's concern
	// (core_exported_api), so the langspec voice stays silent and lets it win.
	assertReason(t, v, lsSym("PublicApi", "class", "java", "public"), f, "")
	assertReason(t, v, lsSym("doThing", "method", "java", "public"), f, "")
}

func TestLangspecVoiceReflectiveType(t *testing.T) {
	v := langspecVoice{lang: "java"}
	// A non-callable with explicit non-public visibility is still reflectively
	// loadable (Class.forName / DI registry), so it never earns `dead`.
	assertReason(t, v, lsSym("Inner", "class", "java", "private"), Facts{}, ReasonLangspecReflectiveType)
	assertReason(t, v, lsSym("Helper", "type", "java", "private"), Facts{}, ReasonLangspecReflectiveType)
	assertReason(t, v, lsSym("MAX", "constant", "java", "private"), Facts{}, ReasonLangspecReflectiveType)
}

func TestLangspecVoiceNonFileLocalCallable(t *testing.T) {
	v := langspecVoice{lang: "java"}
	// protected / internal / package callables are reachable from a subclass, the
	// same package, or the same assembly — not file-local, so they raise a hand.
	for _, vis := range []string{"protected", "internal", "package"} {
		assertReason(t, v, lsSym("m", "method", "java", vis), Facts{}, ReasonLangspecPublicNoFramework)
	}
}

func TestLangspecVoicePHPPrivateIsDynamic(t *testing.T) {
	v := langspecVoice{lang: "php"}
	// PHP reflection (call_user_func / __call / ReflectionMethod) reaches private
	// methods, so privacy is not a closed-world proof — never silent.
	assertReason(t, v, lsSym("helper", "method", "php", "private"), Facts{}, ReasonLangspecDynamic)
	assertReason(t, v, lsSym("util", "function", "php", "private"), Facts{}, ReasonLangspecDynamic)
}

func TestLangspecVoiceUnvalidatedStaticPrivate(t *testing.T) {
	// A statically-typed langspec language with no benchmark repo holds its
	// private callables open-world (ls_unvalidated) until a `dead` tier is validated.
	for _, lang := range []string{"kotlin", "csharp", "scala", "cpp", "c"} {
		v := langspecVoice{lang: lang}
		assertReason(t, v, lsSym("priv", "method", lang, "private"), Facts{}, ReasonLangspecUnvalidated)
		assertReason(t, v, lsSym("priv", "function", lang, "private"), Facts{}, ReasonLangspecUnvalidated)
	}
}

func TestLangspecVoiceJavaPrivateFallsSilent(t *testing.T) {
	v := langspecVoice{lang: "java"}
	// Java is the validated dead-eligible language: an explicitly private callable
	// with no framework/interface/annotation hand falls silent, so the arbiter's
	// soundness gate decides.
	assertReason(t, v, lsSym("helper", "method", "java", "private"), Facts{}, "")
	assertReason(t, v, lsSym("compute", "function", "java", "private"), Facts{}, "")
}

// TestLangspecArbiterTwoSided is the binding control (pitch 25-20): through the
// real registered voice stack, a private, unmentioned, non-annotated Java method
// earns `dead`, while annotated / public / interface-method / mentioned symbols
// stay possibly_dead with their specific reasons — and a non-eligible langspec
// language (C#) never earns `dead`. This pins the per-language decision validated
// against the javalin benchmark (zero false `dead`) plus this synthetic positive.
func TestLangspecArbiterTwoSided(t *testing.T) {
	a := defaultArbiter()
	f := Facts{
		HarvestedLangs:         harvested("java"),
		MentionedNames:         langNames("java", "usedName"),
		LangspecAnnotatedNames: nameSet("handler"),
		InterfaceMethodNames:   nameSet("execute"),
	}
	cands := []Symbol{
		lsSym("deadHelper", "method", "java", "private"), // unmentioned, file-local → dead
		lsSym("handler", "method", "java", "private"),    // annotated → ls_annotated
		lsSym("publicApi", "method", "java", "public"),   // public → ls_public_no_framework
		lsSym("execute", "method", "java", "private"),    // interface method → ls_interface_method
		lsSym("usedName", "method", "java", "private"),   // mentioned → core_name_mentioned
		lsSym("csPriv", "method", "csharp", "private"),   // not eligible → ls_unvalidated, never dead
	}
	got := a.Decide(cands, f)
	want := []struct {
		verdict Verdict
		code    string // "" when dead (no reason)
	}{
		{VerdictDead, ""},
		{VerdictPossiblyDead, ReasonLangspecAnnotated},
		{VerdictPossiblyDead, ReasonLangspecPublicNoFramework},
		{VerdictPossiblyDead, ReasonLangspecInterfaceMethod},
		{VerdictPossiblyDead, ReasonNameMentioned},
		{VerdictPossiblyDead, ReasonLangspecUnvalidated},
	}
	for i, w := range want {
		if got[i].Verdict != w.verdict {
			t.Errorf("%s: verdict = %q, want %q", cands[i].Name, got[i].Verdict, w.verdict)
		}
		if w.code == "" {
			if got[i].Reason != nil {
				t.Errorf("%s: dead must carry no reason, got %+v", cands[i].Name, got[i].Reason)
			}
			continue
		}
		if got[i].Reason == nil || got[i].Reason.Code != w.code {
			t.Errorf("%s: reason = %+v, want %q", cands[i].Name, got[i].Reason, w.code)
		}
	}
}

// TestLangspecJavaNeedsHarvestToEarnDead proves the soundness precondition: the
// same private, unmentioned Java method that earns `dead` when java harvested
// falls closed to core_no_harvest when it did not — `dead` is never earned off an
// absent mention set.
func TestLangspecJavaNeedsHarvestToEarnDead(t *testing.T) {
	a := defaultArbiter()
	sym := lsSym("orphan", "method", "java", "private")
	dead := a.Decide([]Symbol{sym}, Facts{HarvestedLangs: harvested("java"), MentionedNames: langNames("java", "other")})
	if dead[0].Verdict != VerdictDead {
		t.Fatalf("with harvest: got %q, want dead", dead[0].Verdict)
	}
	noHarvest := a.Decide([]Symbol{sym}, Facts{})
	if noHarvest[0].Verdict != VerdictPossiblyDead || noHarvest[0].Reason.Code != ReasonNoHarvest {
		t.Errorf("without harvest: got %q/%+v, want possibly_dead/core_no_harvest", noHarvest[0].Verdict, noHarvest[0].Reason)
	}
}

func TestLangspecDeadEligibilityIsPerLanguage(t *testing.T) {
	// The decision is recorded as data, pinned here: only Java is dead-eligible,
	// PHP is fixed dynamic (reasons-only), and no other langspec language is
	// eligible. This guards against silently widening the `dead` tier.
	if !langspecDeadEligible["java"] {
		t.Error("java must be dead-eligible (validated against javalin)")
	}
	for _, lang := range []string{"kotlin", "csharp", "scala", "cpp", "c", "php"} {
		if langspecDeadEligible[lang] {
			t.Errorf("%s must NOT be dead-eligible (no validated benchmark)", lang)
		}
	}
	if !langspecDynamic["php"] {
		t.Error("php must be fixed reasons-only (reflection-pervasive)")
	}
}
