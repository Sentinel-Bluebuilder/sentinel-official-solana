use distributor_core::address::encode_bech32_address;
use distributor_core::consts::{EMPTY_SHARD_ROOT, ROOTS_DOMAIN_SEPARATOR, SHARD_COUNT};
use distributor_core::hash::{hash160, keccak256};
use distributor_merkle::leaf::{LeafFields, UNREVEALED_PUBKEY, leaf_hash};
use distributor_merkle::sharded::{
    BuildError, BuiltTree, LedgerRecord, build_sharded_tree, roots_commitment,
};
use distributor_merkle::tree::is_valid_proof;

fn revealed(seed: u8, liquid: u64) -> LedgerRecord {
    let public_key = test_public_key(seed);
    LedgerRecord {
        compressed_public_key: Some(public_key),
        bech32_address: encode_bech32_address(&hash160(&public_key)),
        liquid_amount: liquid,
        locked_amount: 0,
        vesting_schedule_hash: [0u8; 32],
    }
}

fn test_public_key(seed: u8) -> [u8; 33] {
    use k256::elliptic_curve::sec1::ToSec1Point;
    let scalar = k256::SecretKey::from_slice(&[seed.max(1); 32]).unwrap();
    let point = scalar.public_key().to_sec1_point(true);
    let mut out = [0u8; 33];
    out.copy_from_slice(point.as_bytes());
    out
}

fn unrevealed(address_bytes: [u8; 20], liquid: u64) -> LedgerRecord {
    LedgerRecord {
        compressed_public_key: None,
        bech32_address: encode_bech32_address(&address_bytes),
        liquid_amount: liquid,
        locked_amount: 0,
        vesting_schedule_hash: [0u8; 32],
    }
}

#[test]
fn builds_shard_count_roots_and_every_leaf_proof_verifies() {
    let records: Vec<LedgerRecord> = (1..=50u8)
        .map(|seed| revealed(seed, u64::from(seed) * 10))
        .collect();
    let tree: BuiltTree = build_sharded_tree(records).unwrap();
    assert_eq!(tree.roots.len(), SHARD_COUNT as usize);
    assert_eq!(tree.leaf_counts.len(), SHARD_COUNT as usize);
    assert_eq!(
        tree.roots_commitment,
        roots_commitment(&tree.roots, &tree.leaf_counts, tree.total_claimable)
    );
    let expected_total: u64 = tree
        .leaves
        .iter()
        .map(|leaf| leaf.liquid_amount + leaf.locked_amount)
        .sum();
    assert_eq!(tree.total_claimable, expected_total);
    let mut recomputed = vec![0u32; SHARD_COUNT as usize];
    for leaf in &tree.leaves {
        recomputed[leaf.shard_id as usize] += 1;
    }
    assert_eq!(tree.leaf_counts, recomputed);
    assert_eq!(
        tree.leaf_counts.iter().sum::<u32>() as usize,
        tree.leaves.len()
    );
    for leaf in &tree.leaves {
        assert!(is_valid_proof(
            &leaf.leaf_hash,
            &leaf.proof,
            &tree.roots[leaf.shard_id as usize]
        ));
    }
}

#[test]
fn shard_id_and_leaf_index_are_assigned_consistently() {
    let records: Vec<LedgerRecord> = (1..=50u8).map(|seed| revealed(seed, 1)).collect();
    let tree = build_sharded_tree(records).unwrap();
    for leaf in &tree.leaves {
        assert_eq!(
            leaf.shard_id,
            distributor_core::shard::shard_id(&leaf.address_bytes)
        );
        let recomputed = leaf_hash(&LeafFields {
            shard_id: leaf.shard_id,
            leaf_index: leaf.leaf_index,
            compressed_public_key: leaf.compressed_public_key,
            bech32_address: &leaf.bech32_address,
            liquid_amount: leaf.liquid_amount,
            locked_amount: leaf.locked_amount,
            vesting_schedule_hash: leaf.vesting_schedule_hash,
        });
        assert_eq!(leaf.leaf_hash, recomputed);
    }
    let mut per_shard: std::collections::BTreeMap<u16, Vec<(u32, [u8; 20])>> = Default::default();
    for leaf in &tree.leaves {
        per_shard
            .entry(leaf.shard_id)
            .or_default()
            .push((leaf.leaf_index, leaf.address_bytes));
    }
    for (_s, mut rows) in per_shard {
        rows.sort_by_key(|(leaf_index, _)| *leaf_index);
        for (position, (leaf_index, _)) in rows.iter().enumerate() {
            assert_eq!(*leaf_index, u32::try_from(position).unwrap());
        }
        let mut by_address = rows.clone();
        by_address.sort_by_key(|(_, address_bytes)| *address_bytes);
        assert_eq!(
            rows, by_address,
            "leaf_index order == address_bytes ascending"
        );
    }
}

#[test]
fn unrevealed_leaf_skips_identity_and_uses_all_zero_public_key() {
    let tree = build_sharded_tree(vec![unrevealed([5u8; 20], 42)]).unwrap();
    assert_eq!(tree.leaves.len(), 1);
    assert_eq!(tree.leaves[0].compressed_public_key, UNREVEALED_PUBKEY);
}

#[test]
fn roots_commitment_is_domain_separated_keccak_over_roots_counts_and_total() {
    assert_eq!(ROOTS_DOMAIN_SEPARATOR, b"sentinel-migration/roots/v1");
    let roots: Vec<[u8; 32]> = (0..SHARD_COUNT)
        .map(|index| [u8::try_from(index).unwrap(); 32])
        .collect();
    let counts: Vec<u32> = (0..SHARD_COUNT).map(|index| u32::from(index) * 3).collect();
    let total_claimable: u64 = 0x1122_3344_5566_7788;
    let roots_len = u32::try_from(roots.len()).unwrap().to_le_bytes();
    let counts_len = u32::try_from(counts.len()).unwrap().to_le_bytes();
    let counts_le: Vec<u8> = counts
        .iter()
        .flat_map(|count| count.to_le_bytes())
        .collect();
    let total_le = total_claimable.to_le_bytes();
    let mut parts: Vec<&[u8]> = vec![ROOTS_DOMAIN_SEPARATOR, &roots_len];
    for root in &roots {
        parts.push(root);
    }
    parts.push(&counts_len);
    parts.push(&counts_le);
    parts.push(&total_le);
    let commitment = roots_commitment(&roots, &counts, total_claimable);
    assert_eq!(commitment, keccak256(&parts));
    assert_eq!(
        commitment,
        [
            0xde, 0xe4, 0xff, 0x5c, 0xea, 0x14, 0xa2, 0xf8, 0x2c, 0x12, 0xa5, 0x64, 0xc1, 0x2a,
            0x24, 0xd8, 0x19, 0xee, 0x51, 0x04, 0x00, 0x24, 0x2b, 0x4e, 0x3e, 0x42, 0xbe, 0x5f,
            0x1e, 0x7a, 0x4c, 0x60
        ]
    );
}

#[test]
fn roots_commitment_changes_when_only_a_leaf_count_changes() {
    let roots: Vec<[u8; 32]> = (0..SHARD_COUNT)
        .map(|index| [u8::try_from(index).unwrap(); 32])
        .collect();
    let counts: Vec<u32> = vec![4; SHARD_COUNT as usize];
    let mut undersized = counts.clone();
    undersized[7] = 3;
    assert_ne!(
        roots_commitment(&roots, &counts, 512),
        roots_commitment(&roots, &undersized, 512)
    );
    let mut oversized = counts.clone();
    oversized[7] = 5;
    assert_ne!(
        roots_commitment(&roots, &counts, 512),
        roots_commitment(&roots, &oversized, 512)
    );
}

#[test]
fn total_claimable_overflow_hard_fails() {
    let first = revealed(1, u64::MAX);
    let second = revealed(2, 1);
    assert!(matches!(
        build_sharded_tree(vec![first, second]),
        Err(BuildError::TotalClaimableOverflow(_))
    ));
}

#[test]
fn empty_shards_use_the_placeholder_empty_root() {
    let tree = build_sharded_tree(vec![revealed(3, 7)]).unwrap();
    let filled = tree.leaves[0].shard_id as usize;
    for (index, root) in tree.roots.iter().enumerate() {
        if index != filled {
            assert_eq!(*root, EMPTY_SHARD_ROOT);
        }
    }
}

#[test]
fn duplicate_address_hard_fails() {
    let record = revealed(9, 1);
    let dup = LedgerRecord {
        compressed_public_key: record.compressed_public_key,
        bech32_address: record.bech32_address.clone(),
        liquid_amount: 2,
        locked_amount: 0,
        vesting_schedule_hash: [0u8; 32],
    };
    assert!(matches!(
        build_sharded_tree(vec![record, dup]),
        Err(BuildError::DuplicateAddress(_))
    ));
}

#[test]
fn public_key_not_matching_address_hard_fails() {
    let mut record = revealed(11, 1);
    record.bech32_address = encode_bech32_address(&[0xAAu8; 20]);
    assert!(matches!(
        build_sharded_tree(vec![record]),
        Err(BuildError::IdentityMismatch(_))
    ));
}

#[test]
fn explicit_unrevealed_public_key_supplied_as_revealed_is_rejected() {
    let record = LedgerRecord {
        compressed_public_key: Some(UNREVEALED_PUBKEY),
        bech32_address: encode_bech32_address(&[1u8; 20]),
        liquid_amount: 1,
        locked_amount: 0,
        vesting_schedule_hash: [0u8; 32],
    };
    assert!(matches!(
        build_sharded_tree(vec![record]),
        Err(BuildError::UnrevealedPublicKeyForbidden(_))
    ));
}

#[test]
fn locked_with_all_zero_schedule_hash_hard_fails() {
    let record = LedgerRecord {
        compressed_public_key: None,
        bech32_address: encode_bech32_address(&[8u8; 20]),
        liquid_amount: 0,
        locked_amount: 5,
        vesting_schedule_hash: [0u8; 32],
    };
    assert!(matches!(
        build_sharded_tree(vec![record]),
        Err(BuildError::MissingScheduleHash(_))
    ));
}
