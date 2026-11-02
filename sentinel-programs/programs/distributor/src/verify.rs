use anchor_lang::prelude::*;
use distributor_core::{Bech32Address, Beneficiary, ProgramId, RootsCommitment};
use distributor_merkle::leaf::{LeafFields, UNREVEALED_PUBKEY, leaf_hash};
use distributor_merkle::tree::is_valid_proof;

use crate::error::DistributorError;

pub struct VerifiedLeaf {
    pub leaf_public_key: [u8; 33],
}

pub struct VerifyInputs<'a> {
    pub beneficiary: &'a Beneficiary,
    pub roots_commitment: &'a RootsCommitment,
    pub program_id: &'a ProgramId,
    pub shard_id: u16,
    pub leaf_index: u32,
    pub bech32_address: &'a Bech32Address,
    pub liquid_amount: u64,
    pub locked_amount: u64,
    pub vesting_schedule_hash: &'a [u8; 32],
    pub proof: &'a [[u8; 32]],
    pub root: &'a [u8; 32],
    pub signature: &'a [u8; 64],
    pub recovery_id: u8,
}

fn recovery_candidates(hint: u8) -> [u8; 2] {
    if hint > 1 {
        [0u8, 1u8]
    } else {
        [hint, hint ^ 1]
    }
}

pub fn recover_and_verify(inputs: &VerifyInputs<'_>) -> Result<VerifiedLeaf> {
    require!(
        distributor_core::shard::shard_id(inputs.bech32_address.address_bytes()) == inputs.shard_id,
        DistributorError::ShardIdMismatch
    );

    let payload = distributor_adr036::payload::claim_payload(
        inputs.beneficiary,
        inputs.roots_commitment,
        inputs.program_id,
    );
    let message_hash = distributor_adr036::sign_doc::sign_doc_hash(inputs.bech32_address, &payload);

    let leaf_hash_with = |public_key: [u8; 33]| {
        leaf_hash(&LeafFields {
            shard_id: inputs.shard_id,
            leaf_index: inputs.leaf_index,
            compressed_public_key: public_key,
            bech32_address: inputs.bech32_address.as_str(),
            liquid_amount: inputs.liquid_amount,
            locked_amount: inputs.locked_amount,
            vesting_schedule_hash: *inputs.vesting_schedule_hash,
        })
    };

    let mut any_recovered = false;
    let mut identity_mismatch = false;
    for recovery_id in recovery_candidates(inputs.recovery_id) {
        let Some(public_key) = distributor_adr036::recover::recover_compressed_public_key(
            &message_hash,
            inputs.signature,
            recovery_id,
        ) else {
            continue;
        };
        any_recovered = true;
        let matches_address = distributor_adr036::identity::is_address_of_public_key(
            &public_key,
            inputs.bech32_address.as_str(),
        );
        if is_valid_proof(&leaf_hash_with(public_key), inputs.proof, inputs.root) {
            if matches_address {
                return Ok(VerifiedLeaf {
                    leaf_public_key: public_key,
                });
            }
            identity_mismatch = true;
            continue;
        }
        if matches_address
            && is_valid_proof(
                &leaf_hash_with(UNREVEALED_PUBKEY),
                inputs.proof,
                inputs.root,
            )
        {
            return Ok(VerifiedLeaf {
                leaf_public_key: UNREVEALED_PUBKEY,
            });
        }
    }

    if identity_mismatch {
        err!(DistributorError::IdentityMismatch)
    } else if any_recovered {
        err!(DistributorError::InvalidProof)
    } else {
        err!(DistributorError::InvalidSignature)
    }
}

#[cfg(test)]
mod tests {
    use super::recovery_candidates;

    #[test]
    fn an_out_of_range_hint_falls_back_to_both() {
        for hint in 2u8..=255 {
            assert_eq!(recovery_candidates(hint), [0, 1]);
        }
    }
}
