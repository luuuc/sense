pub struct Public {
    pub field: String,
}

pub(crate) struct CrateLocal {
    data: String,
}

struct Private {
    data: String,
}

pub(crate) fn crate_helper() {}

pub fn public_api() {
    crate_helper();
}

fn private_impl() {}

pub mod inner {
    pub(super) struct SuperVisible {
        data: String,
    }

    pub(super) fn parent_only() {}

    pub fn inner_public() {
        parent_only();
    }
}
