package conventions

import (
	"strings"

	"github.com/luuuc/sense/internal/model"
)

// refineRubySignificance ranks the informativeness of Ruby inheritance and
// composition conventions, so a project's own architecture outranks the generic
// framework structure the caller already knows. It is the Ruby/Rails sibling of
// the per-language detectors (detectors_go.go, the Rails idioms in
// detectors_framework.go): a post-pass rather than logic baked into the generic
// detectors, so detectors_generic.go stays language-neutral.
//
// It runs over the already-built conventions and sets Significance (an
// ordering-only weight, see Convention.Significance) on the inheritance/
// composition conventions whose instances are Ruby (.rb) files, leaving every
// other convention untouched. The base/mixin target is read from KeySymbol,
// which the generic detectors record as its fully-qualified name.
func refineRubySignificance(conventions []Convention) {
	for i := range conventions {
		c := &conventions[i]
		if c.Category != CategoryInheritance && c.Category != CategoryComposition {
			continue
		}
		if !hasRubyExample(c.Examples) {
			continue
		}
		c.Significance = rubyBaseSignificance(c.KeySymbol)
	}
}

// rubyBaseSignificance scores a base/mixin target by how much it reveals the
// project's own design. A generic Rails framework base ranks lowest (the caller
// already knows it) — checked first, by meaning not syntax, so an explicitly
// qualified framework base (ActionController::Base) is demoted like its bare
// form (ApplicationController) rather than mistaken for a project namespace. A
// remaining namespaced target (Payment::BaseProviderStrategy, LafricaClient::Error)
// is a deliberate sub-architecture and ranks highest; a custom unnamespaced base
// (AdminController, BaseCalculatorService) sits between. Tiers are coarse so that
// within a tier raw prevalence still orders.
func rubyBaseSignificance(qualified string) float64 {
	if model.FrameworkBaseClasses[qualified] {
		return 0.0
	}
	if strings.Contains(qualified, "::") {
		return 2.0
	}
	return 1.0
}

// hasRubyExample reports whether any of a convention's instances is a Ruby
// source file, the signal that the convention is Ruby's to rank.
func hasRubyExample(examples []Example) bool {
	for _, e := range examples {
		if strings.HasSuffix(e.Path, ".rb") {
			return true
		}
	}
	return false
}
