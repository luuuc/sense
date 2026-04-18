pub mod outer {
    pub const MAX: u32 = 10;

    pub struct Config {
        pub name: String,
    }

    pub mod inner {
        pub fn helper() {}

        pub struct Detail;
    }
}

pub fn at_root() {}
