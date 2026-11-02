use solana_sdk::pubkey::Pubkey;

pub struct CosmosKey(k256::ecdsa::SigningKey);

impl CosmosKey {
    pub fn from_seed(seed: u8) -> Self {
        use sha2::{Digest, Sha256};
        let hash = Sha256::digest([seed]);
        let signing_key = k256::ecdsa::SigningKey::from_slice(&hash)
            .expect("seed hash is a valid secp256k1 scalar");
        CosmosKey(signing_key)
    }

    pub fn compressed_public_key(&self) -> [u8; 33] {
        let mut out = [0u8; 33];
        out.copy_from_slice(self.0.verifying_key().to_sec1_point(true).as_bytes());
        out
    }

    pub fn bech32_address(&self) -> String {
        distributor_core::address::encode_bech32_address(&distributor_core::hash::hash160(
            &self.compressed_public_key(),
        ))
    }

    pub fn parsed_bech32_address(&self) -> distributor_core::Bech32Address {
        distributor_core::Bech32Address::parse(&self.bech32_address())
            .expect("encode_bech32_address emits a canonical, JSON-safe address")
    }
}

pub struct ClaimSignature {
    pub signature: [u8; 64],
    pub recovery_id: u8,
}

pub fn sign_claim(
    cosmos: &CosmosKey,
    beneficiary: &Pubkey,
    roots_commitment: &[u8; 32],
    program_id: &Pubkey,
) -> ClaimSignature {
    let payload = distributor_adr036::payload::claim_payload(
        &distributor_core::Beneficiary::from_public_key(*beneficiary),
        &distributor_core::RootsCommitment::from_bytes(*roots_commitment),
        &distributor_core::ProgramId::from_public_key(*program_id),
    );
    let message_hash =
        distributor_adr036::sign_doc::sign_doc_hash(&cosmos.parsed_bech32_address(), &payload);
    let (sig, recovery_id) = cosmos.0.sign_prehash_recoverable(&message_hash);
    let mut signature = [0u8; 64];
    signature.copy_from_slice(&sig.to_bytes());
    ClaimSignature {
        signature,
        recovery_id: recovery_id.to_byte(),
    }
}

pub fn sign_claim_with_address(
    cosmos: &CosmosKey,
    bech32_address: &str,
    beneficiary: &Pubkey,
    roots_commitment: &[u8; 32],
    program_id: &Pubkey,
) -> ClaimSignature {
    let payload = distributor_adr036::payload::claim_payload(
        &distributor_core::Beneficiary::from_public_key(*beneficiary),
        &distributor_core::RootsCommitment::from_bytes(*roots_commitment),
        &distributor_core::ProgramId::from_public_key(*program_id),
    );
    let address = distributor_core::Bech32Address::parse(bech32_address)
        .expect("bech32_address is canonical");
    let message_hash = distributor_adr036::sign_doc::sign_doc_hash(&address, &payload);
    let (sig, recovery_id) = cosmos.0.sign_prehash_recoverable(&message_hash);
    let mut signature = [0u8; 64];
    signature.copy_from_slice(&sig.to_bytes());
    ClaimSignature {
        signature,
        recovery_id: recovery_id.to_byte(),
    }
}

pub fn sign_claim_high_s(
    cosmos: &CosmosKey,
    beneficiary: &Pubkey,
    roots_commitment: &[u8; 32],
    program_id: &Pubkey,
) -> ClaimSignature {
    let low = sign_claim(cosmos, beneficiary, roots_commitment, program_id);
    let sig = k256::ecdsa::Signature::from_slice(&low.signature)
        .expect("sign_claim emits a canonical 64-byte signature");
    let high = k256::ecdsa::Signature::from_scalars(sig.r().to_bytes(), (-sig.s()).to_bytes())
        .expect("negating a nonzero s yields a nonzero s");
    let mut signature = [0u8; 64];
    signature.copy_from_slice(&high.to_bytes());
    ClaimSignature {
        signature,
        recovery_id: low.recovery_id ^ 1,
    }
}

pub fn same_shard_cosmos_pair() -> (CosmosKey, CosmosKey) {
    let mut first_seed_by_shard = std::collections::HashMap::new();
    for seed in 1..=u8::MAX {
        let key = CosmosKey::from_seed(seed);
        let address_bytes = distributor_core::address::decode_bech32_address(&key.bech32_address())
            .expect("derived address is canonical");
        if let Some(first) =
            first_seed_by_shard.insert(distributor_core::shard::shard_id(&address_bytes), seed)
        {
            return (CosmosKey::from_seed(first), key);
        }
    }
    unreachable!("255 seeds over SHARD_COUNT shards must collide")
}

pub fn synth_single_leaf_tree(
    cosmos: &CosmosKey,
    liquid: u64,
    locked: u64,
) -> distributor_merkle::BuiltTree {
    let record = distributor_merkle::LedgerRecord {
        compressed_public_key: Some(cosmos.compressed_public_key()),
        bech32_address: cosmos.bech32_address(),
        liquid_amount: liquid,
        locked_amount: locked,
        vesting_schedule_hash: distributor_vesting::schedule::ZERO_SCHEDULE_HASH,
    };
    distributor_merkle::build_sharded_tree(vec![record]).expect("single-leaf tree should build")
}

pub fn recount_total_claimable(tree: &distributor_merkle::BuiltTree) -> u64 {
    tree.leaves
        .iter()
        .map(|leaf| leaf.liquid_amount + leaf.locked_amount)
        .sum()
}

pub fn recount_shard_leaf_counts(tree: &distributor_merkle::BuiltTree) -> Vec<u32> {
    let mut counts = vec![0u32; distributor_core::consts::SHARD_COUNT as usize];
    for leaf in &tree.leaves {
        counts[leaf.shard_id as usize] += 1;
    }
    assert_eq!(
        counts, tree.leaf_counts,
        "builder leaf_counts must equal the counts implied by the emitted leaves"
    );
    counts
}

pub fn roots_commitment_of(tree: &distributor_merkle::BuiltTree) -> [u8; 32] {
    distributor_merkle::roots_commitment(&tree.roots, &tree.leaf_counts, tree.total_claimable)
}
