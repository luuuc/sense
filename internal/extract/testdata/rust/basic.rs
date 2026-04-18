const MAX_SIZE: u32 = 100;
static VERSION: &str = "1.0";

pub struct Money {
    pub amount: i64,
    currency: String,
}

pub enum Status {
    Active,
    Inactive,
}

pub trait Formatter {
    fn format(&self) -> String;
}

pub type Name = String;

impl Money {
    pub fn new(amount: i64) -> Self {
        Self { amount, currency: String::from("USD") }
    }

    pub fn display(&self) -> String {
        format!("{}", self.amount)
    }
}

impl Formatter for Money {
    fn format(&self) -> String {
        self.display()
    }
}

pub fn process(m: Money) {
    let _ = m;
}
