#[derive(Debug, Clone, PartialEq, Eq, Hash)]
pub struct Token {
    value: String,
}

#[derive(Default)]
pub struct Settings {
    verbose: bool,
    retries: u32,
}

#[derive(Debug)]
pub enum Command {
    Start,
    Stop,
    Restart(Settings),
}

pub trait Encoder {
    fn encode(&self) -> Vec<u8>;
}

#[derive(Debug, Clone)]
pub struct Message {
    payload: Token,
    command: Command,
}

impl Encoder for Message {
    fn encode(&self) -> Vec<u8> {
        Vec::new()
    }
}

impl Message {
    pub fn new(payload: Token) -> Self {
        Self {
            payload,
            command: Command::Start,
        }
    }
}
