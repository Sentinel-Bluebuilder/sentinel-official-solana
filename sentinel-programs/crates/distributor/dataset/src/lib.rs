#![deny(
    clippy::unwrap_used,
    clippy::expect_used,
    clippy::panic,
    clippy::indexing_slicing,
    clippy::arithmetic_side_effects
)]

mod error;
mod types;
mod verify;

pub use error::DatasetError;
pub use types::{
    ATTESTATION_SCHEMA_VERSION, Attestation, ClaimEntry, MAX_ATTESTATION_STRING_BYTES,
    MAX_INDEX_FILE_BYTES, MAX_PROOF_SIBLING_COUNT, MAX_SHARD_FILE_BYTES, ShardDataset,
    decode_hex_array, manifest_hash_of, manifest_hash_of_json,
};
pub use verify::require_valid_dataset;
