package scan

import "github.com/luuuc/sense/internal/extract"

// ---- collector (per-file Emitter) ----

// collector is the per-file extract.Emitter the parse pass writes into. One is
// created per file in parseFileCore; the walk then folds each file's harvested
// name sets into the project-wide accumulators via partitionHarvestedNames.
type collector struct {
	symbols          []extract.EmittedSymbol
	edges            []extract.EmittedEdge
	dispatchNames    []string
	mentionedNames   []string
	cgoExports       []string
	rustExports      []string
	rustTestSymbols  []string
	rustTraitMethods []string
	rustAllowDead    []string
	tsDecorated      []string
	tsDefaultExports []string
	pyDecorated      []string
	pyRoutes         []string
	pyDjango         []string
	pyAllExports     []string
	lsAnnotated      []string
}

func (c *collector) Symbol(s extract.EmittedSymbol) error {
	c.symbols = append(c.symbols, s)
	return nil
}
func (c *collector) Edge(e extract.EmittedEdge) error { c.edges = append(c.edges, e); return nil }

// DispatchName implements extract.DispatchEmitter: an extractor that detects a
// reflective dispatch target (a send/const_get/define_method literal name)
// streams it here. The names are aggregated project-wide into sense_meta so
// the dead-code arbiter can keep a reflectively-reachable symbol open-world.
func (c *collector) DispatchName(name string) error {
	c.dispatchNames = append(c.dispatchNames, name)
	return nil
}

// MentionName implements extract.MentionEmitter: an extractor streams every
// bare name a file mentions (identifier/symbol token, excluding definition
// names). The project-wide union feeds the dead-code arbiter's soundness gate
// so a symbol earns `dead` only when its name is mentioned nowhere a hidden
// caller could be.
func (c *collector) MentionName(name string) error {
	c.mentionedNames = append(c.mentionedNames, name)
	return nil
}

// CgoExportName implements extract.CgoExportEmitter: an extractor streams every
// name marked with a cgo `//export` directive. The project-wide set feeds the
// dead-code Go voice so a function called only from C stays open-world (go_cgo)
// instead of being falsely called dead.
func (c *collector) CgoExportName(name string) error {
	c.cgoExports = append(c.cgoExports, name)
	return nil
}

// RustExportName implements extract.RustHarvestEmitter: an extractor streams every
// Rust function/static whose reachability the edge graph cannot see (a
// `#[no_mangle]` / `#[export_name]` function, a `#[no_mangle]` / `#[used]`
// static). The project-wide set feeds the dead-code Rust voice so such a symbol
// stays open-world (rust_ffi / rust_used) rather than being falsely called dead.
func (c *collector) RustExportName(name string) error {
	c.rustExports = append(c.rustExports, name)
	return nil
}

// RustTestSymbol implements extract.RustHarvestEmitter: an extractor streams every
// Rust test-only symbol (`#[test]` / `#[bench]`, or nested under `#[cfg(test)]`).
// The project-wide set feeds the dead-code Rust voice so a test symbol stays
// open-world (rust_test) instead of being falsely called dead.
func (c *collector) RustTestSymbol(name string) error {
	c.rustTestSymbols = append(c.rustTestSymbols, name)
	return nil
}

// RustTraitImplMethod implements extract.RustHarvestEmitter: an extractor streams
// every method defined in an `impl Trait for Type` block. The project-wide set
// feeds the dead-code Rust voice so a trait-satisfying method stays open-world
// (rust_trait_impl) instead of being falsely called dead.
func (c *collector) RustTraitImplMethod(name string) error {
	c.rustTraitMethods = append(c.rustTraitMethods, name)
	return nil
}

// RustAllowDeadName implements extract.RustHarvestEmitter: an extractor streams
// every item annotated `#[allow(dead_code)]` / `#[allow(unused)]`. The project-wide
// set feeds the dead-code Rust voice so an intentionally-retained symbol stays
// open-world (rust_allow_dead) instead of being called dead — rustc suppresses its
// warning, so it is never in the cargo oracle.
func (c *collector) RustAllowDeadName(name string) error {
	c.rustAllowDead = append(c.rustAllowDead, name)
	return nil
}

// TSDecoratedName implements extract.TSHarvestEmitter: an extractor streams every
// TS/JS class/method carrying a decorator (`@Component` / `@Injectable` / route
// decorator). The project-wide set feeds the dead-code TS voice so a
// framework-dispatched symbol stays open-world (ts_decorator) instead of being
// falsely called dead.
func (c *collector) TSDecoratedName(name string) error {
	c.tsDecorated = append(c.tsDecorated, name)
	return nil
}

// TSDefaultExportName implements extract.TSHarvestEmitter: an extractor streams
// every name bound by an `export default` form. The project-wide set feeds the
// dead-code TS voice so a default export carries the more specific
// ts_default_export reason rather than the generic ts_exported.
func (c *collector) TSDefaultExportName(name string) error {
	c.tsDefaultExports = append(c.tsDefaultExports, name)
	return nil
}

// PythonDecoratedName implements extract.PythonHarvestEmitter: an extractor
// streams every decorated Python function/method/class. The project-wide set
// feeds the dead-code Python voice so a decorator-dispatched symbol stays
// open-world (py_decorator) instead of being falsely called dead.
func (c *collector) PythonDecoratedName(name string) error {
	c.pyDecorated = append(c.pyDecorated, name)
	return nil
}

// PythonRouteName implements extract.PythonHarvestEmitter: an extractor streams
// every handler carrying a route decorator (Flask/FastAPI). The project-wide set
// feeds the Python voice so a framework-routed handler stays open-world
// (py_route) instead of being falsely called dead.
func (c *collector) PythonRouteName(name string) error {
	c.pyRoutes = append(c.pyRoutes, name)
	return nil
}

// PythonDjangoName implements extract.PythonHarvestEmitter: an extractor streams
// every symbol carrying a Django-dispatch decorator (`@receiver`,
// `@admin.register`). The project-wide set feeds the Python voice so a
// signal/admin-dispatched symbol stays open-world (py_django).
func (c *collector) PythonDjangoName(name string) error {
	c.pyDjango = append(c.pyDjango, name)
	return nil
}

// PythonAllExportName implements extract.PythonHarvestEmitter: an extractor
// streams every name a module declares public via `__all__`. The project-wide set
// feeds the Python voice so a declared-public name stays open-world
// (py_all_export) even when underscore-private.
func (c *collector) PythonAllExportName(name string) error {
	c.pyAllExports = append(c.pyAllExports, name)
	return nil
}

// LangspecAnnotatedName implements extract.LangspecHarvestEmitter: the langspec
// extractor streams every annotated/attributed class/method/function (Java
// `@Service`, C# `[Fact]`, Kotlin/Scala annotations, PHP `#[Route]`). The
// project-wide set feeds the dead-code langspec voice so a framework-dispatched
// symbol stays open-world (ls_annotated) instead of being falsely called dead.
func (c *collector) LangspecAnnotatedName(name string) error {
	c.lsAnnotated = append(c.lsAnnotated, name)
	return nil
}

// ---- harvested-name partition ----

// partitionHarvestedNames folds one parsed file's harvested name sets into the
// project-wide accumulators on the harness. It is the soundness-critical seam
// the dead-code arbiter depends on: the two per-language sets (dispatch and
// mention names) are keyed by the file's language so a Ruby file's mentions can
// never land in another language's set, and each flat set (cgo / rust / ts / py
// / langspec) collects only the names its own emitter produced. Lifted out of
// walkTree's phase 4 so the routing lives beside the collector that produced the
// sets, where a contributor adding a new harvested-name kind edits one cohesive
// place.
func (h *harness) partitionHarvestedNames(fr *fileResult) {
	if len(fr.DispatchNames) > 0 {
		if h.dispatchNames == nil {
			h.dispatchNames = map[string]map[string]struct{}{}
		}
		addNamesByLang(h.dispatchNames, fr.Language, fr.DispatchNames)
	}
	if len(fr.MentionedNames) > 0 {
		if h.mentionedNames == nil {
			h.mentionedNames = map[string]map[string]struct{}{}
		}
		addNamesByLang(h.mentionedNames, fr.Language, fr.MentionedNames)
	}
	addFlatNames(&h.cgoExports, fr.CgoExports)
	addFlatNames(&h.rustExports, fr.RustExports)
	addFlatNames(&h.rustTestSymbols, fr.RustTestSymbols)
	addFlatNames(&h.rustTraitMethods, fr.RustTraitMethods)
	addFlatNames(&h.rustAllowDead, fr.RustAllowDead)
	addFlatNames(&h.tsDecorated, fr.TSDecorated)
	addFlatNames(&h.tsDefaultExports, fr.TSDefaultExports)
	addFlatNames(&h.pyDecorated, fr.PyDecorated)
	addFlatNames(&h.pyRoutes, fr.PyRoutes)
	addFlatNames(&h.pyDjango, fr.PyDjango)
	addFlatNames(&h.pyAllExports, fr.PyAllExports)
	addFlatNames(&h.lsAnnotated, fr.LangspecAnnotated)
}

// addFlatNames merges names into a project-wide flat set, lazily allocating the
// map on first use so an absent set never costs an allocation. The dst map is
// passed by pointer precisely so this lazy init is visible to the harness field.
func addFlatNames(dst *map[string]struct{}, names []string) {
	if len(names) == 0 {
		return
	}
	if *dst == nil {
		*dst = map[string]struct{}{}
	}
	for _, n := range names {
		(*dst)[n] = struct{}{}
	}
}
