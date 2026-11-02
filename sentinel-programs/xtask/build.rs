use std::process::Command;

include!("build_support.rs");

const ALWAYS_RERUN_MARKER: &str = "xtask-build-rs-always-rerun-marker";

fn main() {
    assert_marker_absent(std::path::Path::new(ALWAYS_RERUN_MARKER));
    println!("cargo:rerun-if-changed={ALWAYS_RERUN_MARKER}");
    println!("cargo:rustc-env=GIT_COMMIT={}", git_commit_env());
}

fn git_commit_env() -> String {
    let head = git_head_commit();
    if head == "unknown" {
        return head;
    }
    tag_dirty(&head, working_tree_is_dirty())
}

fn git_head_commit() -> String {
    Command::new("git")
        .args(["rev-parse", "HEAD"])
        .output()
        .ok()
        .filter(|output| output.status.success())
        .and_then(|output| String::from_utf8(output.stdout).ok())
        .map(|text| text.trim().to_string())
        .filter(|text| !text.is_empty())
        .unwrap_or_else(|| "unknown".to_string())
}

fn working_tree_is_dirty() -> bool {
    Command::new("git")
        .args(["status", "--porcelain"])
        .output()
        .ok()
        .filter(|output| output.status.success())
        .map(|output| !output.stdout.is_empty())
        .unwrap_or(true)
}
