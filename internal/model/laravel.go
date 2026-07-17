package model

// LaravelRelationVerbs is the canonical set of Eloquent relation-declaring
// methods. A model method whose body returns `$this-><verb>(X::class, …)`
// declares a relationship to X; the PHP extractor turns it into a composes
// edge (Rails `has_many` parity) and the convention detector reads the
// same table. `morphTo` is deliberately absent: it names no target class,
// so there is nothing literal to compose with.
var LaravelRelationVerbs = map[string]bool{
	"hasOne":         true,
	"hasMany":        true,
	"hasOneThrough":  true,
	"hasManyThrough": true,
	"belongsTo":      true,
	"belongsToMany":  true,
	"morphOne":       true,
	"morphMany":      true,
	"morphToMany":    true,
	"morphedByMany":  true,
}

// LaravelFrameworkBaseClasses is the set of well-known Laravel framework
// base classes a generated app extends by default. A class extending one
// of these follows the framework rather than expressing the project's own
// architecture, so convention ranking treats them as the least informative
// inheritance targets. Keyed by both the bare name and the fully-qualified
// form the extractor may emit.
var LaravelFrameworkBaseClasses = map[string]bool{
	"Model":                                 true,
	"Authenticatable":                       true,
	"Controller":                            true,
	"FormRequest":                           true,
	"Mailable":                              true,
	"Notification":                          true,
	"Seeder":                                true,
	"Migration":                             true,
	"Command":                               true,
	"ServiceProvider":                       true,
	"Illuminate\\Database\\Eloquent\\Model": true,
	"Illuminate\\Routing\\Controller":       true,
	"Illuminate\\Foundation\\Http\\FormRequest": true,
	"Illuminate\\Support\\ServiceProvider":      true,
	"Illuminate\\Console\\Command":              true,
}
