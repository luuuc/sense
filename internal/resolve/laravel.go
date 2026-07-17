package resolve

// PHP/Laravel-specific resolution refinement. Kept in its own file (not
// baked into the generic resolver) so the framework convention lives
// beside the language it belongs to, mirroring django.go and
// extract/php/laravel.go. The generic resolver only dispatches to the
// seam declared here.

// phpInheritedDispatch reports whether an unresolved `\`-separated method
// target (`App\Sub\run`, `App\Facades\Payments\charge`) may walk the class
// ancestry in resolveInherited. PHP joins namespace, class, and member
// with the same separator, so the gate is the source file's language, not
// the separator alone - a `\` never appears in another language's
// qualified names, but the language check keeps the dispatch semantics
// (receiver type to the left of the last separator) an explicit PHP
// contract rather than an accident of spelling.
//
// Two dispatch families ride this one gate with zero extra machinery:
//   - real PHP inheritance: `$this->inherited()` emitted as `Sub\inherited`
//     resolves to the nearest `Ancestor\inherited` up the extends chain;
//   - Laravel facades: extract/php/laravel.go emits the facade's accessor
//     as a proxy-IS-A inherits edge, so `Payments::charge()` (target
//     `App\Facades\Payments\charge`) walks Payments → PaymentService and
//     binds the real `PaymentService\charge` method.
func (ix *Index) phpInheritedDispatch(sep string, req Request) bool {
	return sep == `\` && ix.fileLang[req.SourceFileID] == "php"
}
