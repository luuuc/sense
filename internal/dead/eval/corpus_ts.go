package eval

// tsCorpus is the hand-labeled TypeScript / JavaScript slice of the trust
// corpus. Like the Ruby corpus it pins the verdict a *trustworthy* engine must
// produce, and it deliberately includes the hard export-reachable cases the
// naive "unreferenced export" rule would lie about (a JSX component used
// cross-file, a Next.js route entry, a decorator-dispatched class) alongside
// the one shape that earns `dead` (a module-private symbol) and its
// JavaScript counterpart that must NOT (the precision split).
//
// Each fixture is its own isolated project, so it scans as a library
// (no entry point, no framework): an exported callable therefore reads as
// the core voice's core_exported_api — observably possibly_dead, which is all
// the verdict-level harness scores. The reason-level two-sided control lives in
// the dead package's golden test.
//
// Ground-truth rule for TS/JS: only a module-private (non-exported) `.ts`/`.tsx`
// symbol that is unmentioned and reached by no framework idiom earns `dead`.
// Every exported symbol, every framework-reached symbol, and every plain
// JavaScript symbol stays possibly_dead.
func tsCorpus() []Fixture {
	return []Fixture{
		{
			Name: "ts_module_private_dead",
			Files: map[string]string{
				"widget.ts": `export function renderWidget(): string {
  return formatLabel();
}

function formatLabel(): string {
  return "label";
}

function orphanedWidget(): number {
  return 42;
}
`,
			},
			Want: []Sym{
				{"orphanedWidget", Dead,
					"module-private, zero callers, name mentioned nowhere — the earned dead"},
				{"formatLabel", Alive,
					"called by renderWidget in the same module"},
				{"renderWidget", PossiblyDead,
					"exported — a re-export / dynamic import / external consumer may reach it"},
			},
		},
		{
			Name: "ts_jsx_component",
			Files: map[string]string{
				"Card.tsx": `export function Card(): null {
  return null;
}
`,
			},
			Want: []Sym{
				{"Card", PossiblyDead,
					"exported PascalCase component in a .tsx file — may be rendered as <Card/> elsewhere (ts_jsx)"},
			},
		},
		{
			Name: "ts_next_route",
			Files: map[string]string{
				"app/dashboard/page.tsx": `export default function Page(): null {
  return null;
}
`,
			},
			Want: []Sym{
				{"Page", PossiblyDead,
					"Next.js app/ route default export — file-system routing renders it with no edge (ts_framework_route)"},
			},
		},
		{
			Name: "ts_decorator_module_private",
			Files: map[string]string{
				"mailer.ts": `export function configureMailer(): void {}

@Injectable()
class MailerService {}
`,
			},
			Want: []Sym{
				{"MailerService", PossiblyDead,
					"module-private but @Injectable — a DI container instantiates it with no source caller (ts_decorator)"},
			},
		},
		{
			Name: "js_module_private_conservative",
			Files: map[string]string{
				"legacy.js": `function orphanedLegacy() {
  return 42;
}
`,
			},
			Want: []Sym{
				{"orphanedLegacy", PossiblyDead,
					"byte-identical to the .ts dead shape, but plain JS is held open-world (js_dynamic) — the precision split"},
			},
		},
	}
}
