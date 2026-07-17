package php

// phpCommonNames is PHP's common-name set for the receiver/confidence
// law's clause 2 (decision 0003): a bare-name fallback edge is never
// emitted for these method names without a type witness, because a
// receiverless match on them is overwhelmingly false - they are the
// builtin-shadowing and framework-utility names every PHP/Laravel codebase
// calls constantly (`$x->get()`, `$x->save()`, `$q->where()`), the exact
// shape the Django findings 1/6/12 measured as junk fans. Per-language
// data owned by this package, never shared across languages. Keys are
// lower-case and callers fold the name before the lookup - PHP method
// dispatch is case-insensitive, so `$x->ToArray()` and `$x->toarray()`
// are the same method and must hit the same guard.
var phpCommonNames = map[string]bool{
	"add": true, "all": true, "count": true, "create": true, "delete": true,
	"each": true, "filter": true, "find": true, "first": true, "fire": true,
	"get": true, "handle": true, "has": true, "id": true, "items": true,
	"json": true, "key": true, "last": true, "list": true, "make": true,
	"map": true, "name": true, "pop": true, "push": true, "query": true,
	"remove": true, "render": true, "run": true, "save": true, "set": true,
	"toarray": true, "type": true, "update": true, "value": true, "where": true,
}
