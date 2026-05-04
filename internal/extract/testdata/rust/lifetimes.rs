pub struct Borrowed<'a> {
    data: &'a str,
    owner: &'a Config,
}

pub struct Config {
    path: String,
}

impl<'a> Borrowed<'a> {
    pub fn new(data: &'a str, owner: &'a Config) -> Self {
        Self { data, owner }
    }

    pub fn value(&self) -> &str {
        self.data
    }
}

pub fn process<'a>(b: &'a Borrowed<'a>) -> &'a str {
    b.value()
}
