use crate::DatasetError;
use distributor_core::hash::keccak256;
use distributor_merkle::leaf::UNREVEALED_PUBKEY;
use distributor_vesting::schedule::Schedule;
use serde::{Deserialize, Serialize};

mod u64_str {
    use serde::{Deserialize, Deserializer, Serializer};

    #[allow(clippy::trivially_copy_pass_by_ref)]
    pub(super) fn serialize<S: Serializer>(value: &u64, serializer: S) -> Result<S::Ok, S::Error> {
        serializer.serialize_str(&value.to_string())
    }
    pub(super) fn deserialize<'de, D: Deserializer<'de>>(deserializer: D) -> Result<u64, D::Error> {
        String::deserialize(deserializer)?
            .parse()
            .map_err(serde::de::Error::custom)
    }
}

pub fn decode_hex_array<const N: usize>(text: &str) -> Result<[u8; N], DatasetError> {
    let bytes = hex::decode(text)?;
    let actual = bytes.len();
    bytes
        .try_into()
        .map_err(|_| DatasetError::InvalidHexLength {
            expected: N,
            actual,
        })
}

#[derive(Serialize, Deserialize, Clone, Debug)]
#[serde(deny_unknown_fields)]
pub struct ClaimEntry {
    pub bech32_address: String,
    pub public_key: Option<String>,
    #[serde(with = "u64_str")]
    pub liquid_amount: u64,
    #[serde(with = "u64_str")]
    pub locked_amount: u64,
    pub schedule: Option<Schedule>,
    pub shard_id: u16,
    pub leaf_index: u32,
    pub vesting_schedule_hash: String,
    pub proof: Vec<String>,
}

impl ClaimEntry {
    pub fn leaf_hash(&self) -> Result<[u8; 32], DatasetError> {
        let compressed_public_key = match &self.public_key {
            Some(public_key_hex) => decode_hex_array::<33>(public_key_hex)?,
            None => UNREVEALED_PUBKEY,
        };
        Ok(distributor_merkle::leaf::leaf_hash(
            &distributor_merkle::leaf::LeafFields {
                shard_id: self.shard_id,
                leaf_index: self.leaf_index,
                compressed_public_key,
                bech32_address: &self.bech32_address,
                liquid_amount: self.liquid_amount,
                locked_amount: self.locked_amount,
                vesting_schedule_hash: decode_hex_array::<32>(&self.vesting_schedule_hash)?,
            },
        ))
    }
}

#[derive(Serialize, Deserialize, Clone, Debug)]
#[serde(deny_unknown_fields)]
pub struct ShardDataset {
    pub shard_id: u16,
    pub entries: Vec<ClaimEntry>,
}

pub const ATTESTATION_SCHEMA_VERSION: u16 = 3;
pub const MAX_INDEX_FILE_BYTES: u64 = 16_777_216;
pub const MAX_SHARD_FILE_BYTES: u64 = 268_435_456;
pub const MAX_PROOF_SIBLING_COUNT: usize = 16;
pub const MAX_ATTESTATION_STRING_BYTES: usize = 128;

#[derive(Serialize, Deserialize, Clone, Debug)]
#[serde(deny_unknown_fields)]
pub struct Attestation {
    pub schema_version: u16,
    pub roots_commitment: String,
    pub shard_leaf_counts: Vec<u32>,
    #[serde(with = "u64_str")]
    pub total_claimable: u64,
    pub leaf_count: u64,
    pub shard_count: u16,
    pub ledger_content_hash: String,
    pub claim_dataset_manifest_hash: String,
    pub git_commit: String,
    pub tool_version: String,
}

#[must_use]
pub fn manifest_hash_of_json<'a>(shards: impl IntoIterator<Item = (u16, &'a [u8])>) -> [u8; 32] {
    let mut acc: Vec<u8> = Vec::new();
    for (shard_id, json) in shards {
        acc.extend_from_slice(&shard_id.to_le_bytes());
        acc.extend_from_slice(&keccak256(&[json]));
    }
    keccak256(&[&acc])
}

#[must_use]
pub fn manifest_hash_of(shards: &[ShardDataset]) -> [u8; 32] {
    #[allow(clippy::expect_used)]
    let jsons: Vec<Vec<u8>> = shards
        .iter()
        .map(|shard| {
            serde_json::to_vec(&shard.entries).expect("ShardDataset entries always serialize")
        })
        .collect();
    manifest_hash_of_json(
        shards
            .iter()
            .zip(&jsons)
            .map(|(shard, json)| (shard.shard_id, json.as_slice())),
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn attestation_rejects_unknown_fields() {
        let good = r#"{"schema_version":3,"roots_commitment":"ab","shard_leaf_counts":[1,0],"total_claimable":"1","leaf_count":1,"shard_count":128,"ledger_content_hash":"cd","claim_dataset_manifest_hash":"ef","git_commit":"g","tool_version":"v"}"#;
        assert!(serde_json::from_str::<Attestation>(good).is_ok());
        let extra = r#"{"schema_version":3,"roots_commitment":"ab","shard_leaf_counts":[1,0],"total_claimable":"1","leaf_count":1,"shard_count":128,"ledger_content_hash":"cd","claim_dataset_manifest_hash":"ef","git_commit":"g","tool_version":"v","surprise":1}"#;
        assert!(serde_json::from_str::<Attestation>(extra).is_err());
    }

    #[test]
    fn attestation_rejects_the_v1_schema() {
        let v1 = r#"{"roots_commitment":"ab","sum_leaves":"1","leaf_count":1,"shard_count":128,"ledger_content_hash":"cd","claim_dataset_manifest_hash":"ef","git_commit":"g","tool_version":"v"}"#;
        assert!(serde_json::from_str::<Attestation>(v1).is_err());
    }
}
