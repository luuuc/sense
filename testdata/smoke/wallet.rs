// orphaned_rust is non-pub, has no caller, and its name is mentioned nowhere
// else — the genuinely-dead shape the Rust voice earns `dead` for.
fn orphaned_rust() {}

pub trait Greeter {
    fn greet(&self);
}

struct Robot;

impl Greeter for Robot {
    // greet implements a trait method (Greeter::greet is in the index), so the
    // Rust voice keeps it possibly_dead with reason rust_trait_impl even though
    // no caller invokes it directly — it is reached through the trait.
    fn greet(&self) {}
}

struct Money {
    cents: i64,
}

impl Money {
    // clone shares a name with the Clone derive's synthesized method. The Rust
    // voice keeps it possibly_dead (rust_derive), proving derives do not flood
    // the dead set — without the voice the soundness gate would call it dead.
    fn clone(&self) -> Money {
        Money { cents: self.cents }
    }
}

// Registry holds Robot and Money as fields, giving each an incoming edge so they
// are not dead candidates and their methods (greet / clone) surface with the
// Rust voice's reason rather than being rolled up into a dead struct.
pub struct Registry {
    bot: Robot,
    wallet: Money,
}
