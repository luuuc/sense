// depth.rs — Rust depth extraction: visibility, trait impls, derive
// macros, trait method resolution, and struct field composition.

pub const MAX_ITEMS: u32 = 100;
const DEFAULT_NAME: &str = "unknown";

// Trait with method signatures.

pub trait Processor {
    fn process(&self) -> bool;
    fn name(&self) -> &str;
}

pub trait Validator {
    fn validate(&self) -> bool;
}

// Struct with field composition and derive macros.

#[derive(Debug, Clone)]
pub struct Order {
    pub id: u64,
    items: Vec<Item>,
    status: OrderStatus,
    name: String,
    meta: Option<Metadata>,
}

pub struct Item {
    pub label: String,
    weight: f64,
}

pub struct OrderStatus {
    code: u32,
}

pub struct Metadata {
    key: String,
}

// Inherent impl — methods belong to Order.

impl Order {
    pub fn new(id: u64) -> Self {
        Self {
            id,
            items: Vec::new(),
            status: OrderStatus { code: 0 },
            name: String::from("default"),
            meta: None,
        }
    }

    fn total_weight(&self) -> f64 {
        0.0
    }
}

// Trait impl — methods belong to Order, inherits edge to Processor.

impl Processor for Order {
    fn process(&self) -> bool {
        self.validate_internal()
    }

    fn name(&self) -> &str {
        &self.name
    }
}

impl Validator for Order {
    fn validate(&self) -> bool {
        self.process()
    }
}

// Inherent method calls on self resolve to Type::method.

impl Item {
    pub fn describe(&self) -> String {
        self.format_label()
    }

    fn format_label(&self) -> String {
        String::new()
    }
}

fn validate_internal() -> bool {
    true
}

// Enum with variant field compositions.

pub enum Shape {
    Circle(Radius),
    Rectangle { width: Dimension, height: Dimension },
    Nothing,
}

struct Radius {
    value: f64,
}

struct Dimension {
    value: f64,
}

// Visibility: pub(crate) is private.

pub(crate) struct InternalHelper {
    data: String,
}

pub(crate) fn internal_process() {}

// Scoped derive paths.

#[derive(Debug, serde::Serialize)]
pub struct Config {
    pub path: String,
}

// Trait with default method implementation.

pub trait Describable {
    fn describe(&self) -> String;
    fn short_name(&self) -> &str;
}

// Module-scoped trait resolution.

pub mod engine {
    pub trait Runner {
        fn run(&self);
    }

    pub struct Motor;

    impl Runner for Motor {
        fn run(&self) {
            self.ignite()
        }
    }

    impl Motor {
        fn ignite(&self) {}
    }
}

// Ambiguous trait resolution: two traits declare the same method.
// self.execute() should fall back to Type::execute (inherent) at 0.9,
// not pick either trait arbitrarily.

trait Alpha {
    fn execute(&self);
}

trait Beta {
    fn execute(&self);
}

struct Agent;

impl Alpha for Agent {
    fn execute(&self) {}
}

impl Beta for Agent {
    fn execute(&self) {
        self.execute()
    }
}
