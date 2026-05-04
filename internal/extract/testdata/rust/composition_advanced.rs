pub struct Point {
    x: f64,
    y: f64,
}

pub struct Label {
    text: String,
}

pub struct Container {
    boxed: Box<Point>,
    optional: Option<Label>,
    items: Vec<Point>,
    locked: std::sync::Mutex<Label>,
    pair: (Point, Label),
    reference: &'static Point,
}

pub enum Result {
    Ok(Point),
    Error { source: Label, code: u32 },
    Pair(Point, Label),
    Empty,
}

pub struct Wrapper {
    inner: Arc<Point>,
}

pub trait Transformer {
    fn transform(&self, input: Point) -> Label;
}

impl Container {
    pub fn first(&self) -> &Point {
        self.boxed.as_ref()
    }
}

impl Transformer for Container {
    fn transform(&self, input: Point) -> Label {
        self.first();
        Label { text: String::new() }
    }
}

use std::sync::Arc;
