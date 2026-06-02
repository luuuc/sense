"""Python smoke fixture for the dead-code arbiter's two-sided gate.

It pins the one shape that EARNS `dead` (an underscore-private, unmentioned,
non-dunder, non-decorated function) against the shapes that must stay
`possibly_dead`: a dunder, a decorated method, a route handler, a public symbol,
and an underscore-private name kept open by the mention gate.
"""


def _orphaned_helper():
    # Underscore-private, zero callers, mentioned nowhere, no dunder/decorator
    # idiom — the single earned `dead`.
    return 1


def _used_private(payload):
    # Underscore-private but CALLED by public_handler, so it carries an incoming
    # edge and is never a candidate (proves the underscore alone earns nothing).
    return payload * 2


def public_handler(payload):
    return _used_private(payload)


def registry():
    # _mentioned_private appears here as a value (not a call), leaving a textual
    # mention but no resolved edge — so the mention gate keeps it open-world
    # (core_name_mentioned), proving soundness rests on the gate, not the underscore.
    return {"handler": _mentioned_private}


def _mentioned_private():
    return 2


@app.route("/health")
def health_check():
    # Route handler dispatched by the framework's router with no source caller.
    return "ok"


class Account:
    def __init__(self, owner):
        # Dunder: invoked by the interpreter on construction, never by a caller.
        self.owner = owner

    def __repr__(self):
        # Dunder: invoked by the interpreter (repr/print), never by a caller.
        return self.owner

    @property
    def label(self):
        # Decorated: @property turns this into an attribute access.
        return self.owner


def make_account(owner):
    # Keeps Account referenced (alive) so its methods are judged individually.
    return Account(owner)
