#![deny(clippy::indexing_slicing, clippy::arithmetic_side_effects)]
#![allow(clippy::unwrap_used, clippy::expect_used, clippy::panic)]

pub mod build_tree;
pub mod ledger;
pub mod rehearse;
pub mod stage;
pub mod synth;

#[cfg(test)]
mod build_support_tests {
    include!("../build_support.rs");

    #[test]
    fn tag_dirty_appends_suffix_only_when_dirty() {
        assert_eq!(tag_dirty("abc123", false), "abc123");
        assert_eq!(tag_dirty("abc123", true), "abc123-dirty");
    }

    #[test]
    fn assert_marker_absent_passes_when_missing_and_panics_when_spoofed() {
        let dir = tempfile::tempdir().unwrap();
        let marker = dir.path().join("xtask-build-rs-always-rerun-marker");
        assert_marker_absent(&marker);

        std::fs::write(&marker, b"spoof").unwrap();
        let result = std::panic::catch_unwind(|| assert_marker_absent(&marker));
        assert!(
            result.is_err(),
            "must panic when the marker path is spoofed (exists), so a spoof fails the \
             build loudly instead of silently disabling always-rerun"
        );
    }
}
