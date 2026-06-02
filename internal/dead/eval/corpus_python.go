package eval

// pythonCorpus is the hand-labeled Python slice of the trust corpus. Like the
// Ruby and TS slices it pins the verdict a *trustworthy* engine must produce,
// and it deliberately includes the hard invisible-reach cases that the naive
// "unreferenced symbol" rule would lie about — a Django signal receiver, a model
// class, a dunder protocol method, an `__all__`-exported private — alongside the
// one shape that earns `dead` (an underscore-private, unmentioned function) and
// the alive controls.
//
// Each fixture is its own isolated project, so it scans as a library (no entry
// point, no framework): a public callable therefore reads as the core voice's
// core_exported_api — observably possibly_dead, which is all the verdict-level
// harness scores. The reason-level two-sided control lives in the dead package's
// golden test.
//
// Ground-truth rule for Python (pitch 25-19): only a leading-underscore
// (convention- or mangling-private) function/method that is not dunder, not
// decorated/framework-reached, not in `__all__`, and mentioned nowhere may earn
// `dead`. Every public symbol, every class, every constant, and every
// framework-reached symbol stays possibly_dead — the precision discipline the
// Ruby retro earned.
func pythonCorpus() []Fixture {
	return []Fixture{
		{
			// The one earned `dead`, plus the alive private (proving the underscore
			// alone earns nothing) and the public entry (a hidden duck-typed caller
			// could exist).
			Name: "python_underscore_private_dead",
			Files: map[string]string{
				"report.py": `def render_report():
    return _format_row()


def _format_row():
    return "row"


def _orphaned_helper():
    return "no caller, private, mentioned nowhere"
`,
			},
			Want: []Sym{
				{"_orphaned_helper", Dead,
					"underscore-private, zero callers, name mentioned nowhere — the earned dead"},
				{"_format_row", Alive,
					"called by render_report — the underscore alone earns nothing"},
				{"render_report", PossiblyDead,
					"public, unreferenced — a hidden duck-typed caller could exist"},
			},
		},
		{
			// The planted false-dead control: a Django signal receiver is
			// underscore-private AND unmentioned, so it would earn `dead` without the
			// decorator harvest. Django's signal machinery invokes it invisibly, so a
			// trustworthy engine must keep it possibly_dead. If this ever flips to
			// `dead`, the precision gate fails — which is the point.
			Name: "python_signal_receiver",
			Files: map[string]string{
				"signals.py": `from django.dispatch import receiver
from django.db.models.signals import post_save


@receiver(post_save, sender=User)
def _on_user_saved(sender, **kwargs):
    return None
`,
			},
			Want: []Sym{
				{"_on_user_saved", PossiblyDead,
					"Django @receiver signal handler — invoked invisibly by the framework, never a false dead"},
			},
		},
		{
			// A dunder protocol method is invoked by the interpreter, never by a
			// caller — it must never earn `dead` even though it is unreferenced.
			// Cache is referenced by build_cache so it is alive (not a candidate) and
			// __getitem__ is judged on its own rather than rolled up under a dead class.
			Name: "python_dunder_protocol",
			Files: map[string]string{
				"cache.py": `class Cache:
    def __getitem__(self, key):
        return key


def build_cache():
    return Cache()
`,
			},
			Want: []Sym{
				{"Cache.__getitem__", PossiblyDead,
					"dunder protocol method — invoked by the interpreter on subscription"},
				{"Cache", Alive,
					"referenced by build_cache — not a candidate"},
				{"build_cache", PossiblyDead,
					"public factory, unreferenced — a hidden caller could exist"},
			},
		},
		{
			// A Django model class is ORM-driven and reached by name; its fields are
			// not even symbols. The class must stay possibly_dead.
			Name: "python_model_class",
			Files: map[string]string{
				"models.py": `class Article(models.Model):
    title = models.CharField(max_length=200)
    author = models.ForeignKey(User, on_delete=models.CASCADE)
`,
			},
			Want: []Sym{
				{"Article", PossiblyDead,
					"Django model class — ORM-driven, reached by name, never a false dead"},
			},
		},
		{
			// `__all__` overrides the underscore convention: a `_internal_api` listed
			// there is re-exported by `from module import *`. The name appears only as
			// a string literal, so the broad mention set misses it — the dedicated
			// `__all__` harvest is what keeps it possibly_dead.
			Name: "python_all_export_override",
			Files: map[string]string{
				"api.py": `__all__ = ["_internal_api"]


def _internal_api():
    return 1
`,
			},
			Want: []Sym{
				{"_internal_api", PossiblyDead,
					"listed in __all__ — declared public API, re-exported by import *"},
			},
		},
	}
}
