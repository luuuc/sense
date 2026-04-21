// depth_negatives.rs — patterns that should NOT produce depth edges.

// Proc macros: opaque to tree-sitter, should not emit inherits.
#[tokio::main]
async fn main() {}

// Generic bounds: T: Serialize + Clone should NOT emit inherits
// for the function or resolve calls through bounds.
pub fn process_generic<T: Clone>(item: T) {}

// Struct fields with only primitives and std types: no composes edges.
pub struct AllPrimitives {
    a: u8,
    b: i32,
    c: f64,
    d: bool,
    e: char,
    f: usize,
}

pub struct AllStdTypes {
    a: String,
    b: Vec<String>,
    c: Option<u32>,
    d: HashMap<String, String>,
    e: Box<Vec<u32>>,
}

// Cross-crate trait: impl for external trait should emit inherits
// to the trait name, but no trait method resolution (trait not local).
pub trait LocalTrait {
    fn local_method(&self);
}

pub struct Widget;

impl std::fmt::Display for Widget {
    fn fmt(&self, f: &mut std::fmt::Formatter) -> std::fmt::Result {
        Ok(())
    }
}

// Derive with no traits: empty derive should emit nothing.
#[derive()]
pub struct Empty;

// cfg attribute: not a derive, should not emit inherits.
#[cfg(test)]
pub struct TestOnly;
