pub(crate) fn tag_dirty(head: &str, working_tree_dirty: bool) -> String {
    if working_tree_dirty {
        format!("{head}-dirty")
    } else {
        head.to_string()
    }
}

pub(crate) fn assert_marker_absent(path: &std::path::Path) {
    assert!(
        !path.exists(),
        "{} exists -- this would silently disable the always-rerun idiom \
         (cargo:rerun-if-changed points at a path that must never exist) and let a stale \
         dirty/clean GIT_COMMIT tag stick; remove it",
        path.display()
    );
}
