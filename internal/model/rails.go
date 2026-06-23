package model

// RailsCallbackNames is the canonical set of Rails lifecycle callback
// method names. Both the Ruby extractor and the convention detector
// reference this single source of truth.
var RailsCallbackNames = map[string]bool{
	"before_action":     true,
	"after_action":      true,
	"around_action":     true,
	"before_save":       true,
	"after_save":        true,
	"around_save":       true,
	"before_create":     true,
	"after_create":      true,
	"around_create":     true,
	"before_update":     true,
	"after_update":      true,
	"around_update":     true,
	"before_destroy":    true,
	"after_destroy":     true,
	"around_destroy":    true,
	"before_validation": true,
	"after_validation":  true,
	// `validate :method` registers a custom validation predicate the framework
	// invokes during validation — a calls edge to the named method, just like
	// the lifecycle hooks above. Without it the predicate reads as zero-edge.
	// (`validates :attr, ...` takes attribute names, not method symbols, so it
	// is deliberately omitted — it would emit edges to non-method targets.)
	"validate":       true,
	"before_commit":  true,
	"after_commit":   true,
	"after_rollback": true,
}

// FrameworkBaseClasses is the set of well-known Rails framework base classes a
// generated app inherits from by default. A class extending one of these is
// following the framework, not expressing the project's own architecture, so
// convention ranking treats them as the least informative inheritance targets —
// the caller already knows them from Rails itself. Keyed by both the bare name
// and the fully-qualified form the extractor may emit.
var FrameworkBaseClasses = map[string]bool{
	"ApplicationRecord":      true,
	"ApplicationController":  true,
	"ApplicationJob":         true,
	"ApplicationMailer":      true,
	"ApplicationMailbox":     true,
	"ActiveRecord::Base":     true,
	"ActionController::Base": true,
	"ActionController::API":  true,
	"ActionMailer::Base":     true,
	"ActiveJob::Base":        true,
}
