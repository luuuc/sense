// negatives.rs pins Rust Tier-Basic call-emission skip behaviour as
// an artefact rather than a commit message. None of the calls in
// `skipped` produce a calls edge — the golden's empty edges array is
// the assertion.

pub fn skipped() {
    format!("macro invocation — macro_invocation node, not call_expression");
    println!("same");
    foo::<i32>();
    (closure)();
}

pub fn foo<T>() {}
