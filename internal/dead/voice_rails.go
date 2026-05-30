package dead

// railsVoice is the framework voice for Rails. It is gated on Rails being a
// detected framework (Facts.Frameworks["Rails"]); on any other stack it
// raises nothing, so a Ruby-but-not-Rails project sees only the rubyVoice's
// reasons. It re-expresses the Rails-specific reach the old cascade excluded
// as entry points — routed controller actions, framework lifecycle callbacks,
// and concern mixins — as open-world reasons, so they surface as honest
// possibly_dead with a Rails-specific verify recipe instead of vanishing.
//
// Its Lang() is "ruby": Rails is Ruby, and a voice's language both scopes it
// to matching symbols and registers that language as one Sense can reason
// about. Registering "ruby" here is harmless next to rubyVoice — the arbiter's
// language set is idempotent.
type railsVoice struct{}

func (railsVoice) Lang() string { return "ruby" }

func (railsVoice) Inspect(s Symbol, f Facts) *Reason {
	if _, ok := f.Frameworks["Rails"]; !ok {
		return nil
	}
	// A method on a module included into a *Controller is a routed action via
	// the concern. Checked first so it gets the more-specific concern reason
	// (which points the agent at includers) rather than the generic routing
	// reason — both keep it possibly_dead, only the hint differs.
	if s.Kind == "method" && s.ParentID != nil {
		if _, ok := f.ControllerConcernIDs[*s.ParentID]; ok {
			return reasonPtr(ReasonRailsConcern)
		}
	}
	// A controller class or routed action is dispatched by the router, never
	// called from Ruby.
	if isRailsControllerClass(s) || isRailsControllerAction(s, f.ControllerConcernIDs) {
		return reasonPtr(ReasonRailsRouting)
	}
	// A method whose name is a Rails lifecycle callback is invoked by the
	// framework (by symbol or convention), not by application code.
	if isRailsCallbackName(s) {
		return reasonPtr(ReasonRailsCallback)
	}
	return nil
}

// isRailsCallbackName reports whether s is a method whose name is a Rails
// lifecycle hook the framework invokes (before_action, after_save, included,
// …). Reuses the surviving frameworkHooks / railsHooks tables.
func isRailsCallbackName(s Symbol) bool {
	if s.Kind != "method" {
		return false
	}
	if _, ok := frameworkHooks[s.Name]; ok {
		return true
	}
	_, ok := railsHooks[s.Name]
	return ok
}

func init() {
	registerReasons(map[string]reasonSpec{
		ReasonRailsRouting: {
			priority: 20,
			hint:     "Rails controller action dispatched by config/routes, never called from Ruby; check routes before removing",
			verify:   "Controller actions are dispatched by the router. For each, grep config/routes.rb for the controller/action and check for a matching view before removing.",
		},
		ReasonRailsCallback: {
			priority: 20,
			hint:     "Rails lifecycle callback invoked by the framework (before_action/after_save/…); confirm no registration before removing",
			verify:   "Framework callbacks are invoked by Rails, not application code. For each, grep for the method name as a callback registration symbol (e.g. before_action :name) before removing.",
		},
		ReasonRailsConcern: {
			priority: 30,
			hint:     "method on a concern mixed into a controller; becomes a routed action of the includer — check includers before removing",
			verify:   "Concern methods become instance methods of the including controller. Find `include <Concern>` in controllers and check routes before removing.",
		},
	})
}
