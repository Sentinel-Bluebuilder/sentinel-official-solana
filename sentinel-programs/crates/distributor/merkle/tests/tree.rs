use distributor_core::consts::{NODE_TAG, TREE_DOMAIN_SEPARATOR};
use distributor_core::hash::keccak256;
use distributor_merkle::tree::{Levels, is_valid_proof, merkle_proof, merkle_root, node_hash};

fn filled_hash(fill_byte: u8) -> [u8; 32] {
    [fill_byte; 32]
}

#[test]
fn node_hash_is_sorted_pair_and_order_independent() {
    let left = filled_hash(1);
    let right = filled_hash(2);
    let want = keccak256(&[&[NODE_TAG], TREE_DOMAIN_SEPARATOR, &left, &right]);
    assert_eq!(node_hash(&left, &right), want);
    assert_eq!(node_hash(&left, &right), node_hash(&right, &left));
}

#[test]
fn roots_for_small_trees() {
    assert_eq!(merkle_root(&[filled_hash(9)]), filled_hash(9));
    assert_eq!(
        merkle_root(&[filled_hash(1), filled_hash(2)]),
        node_hash(&filled_hash(1), &filled_hash(2))
    );
    assert_eq!(
        merkle_root(&[filled_hash(1), filled_hash(2), filled_hash(3)]),
        node_hash(
            &node_hash(&filled_hash(1), &filled_hash(2)),
            &filled_hash(3)
        )
    );
}

#[test]
fn every_leaf_proof_verifies_for_various_sizes() {
    for count in 1..=33usize {
        let leaves: Vec<[u8; 32]> = (0..count)
            .map(|index| filled_hash(u8::try_from(index).unwrap()))
            .collect();
        let root = merkle_root(&leaves);
        for (index, leaf) in leaves.iter().enumerate() {
            assert!(
                is_valid_proof(leaf, &merkle_proof(&leaves, index), &root),
                "n={count} i={index}"
            );
        }
    }
}

#[test]
fn tampered_proof_and_wrong_leaf_fail() {
    let leaves: Vec<[u8; 32]> = (0..8).map(filled_hash).collect();
    let root = merkle_root(&leaves);
    let mut proof = merkle_proof(&leaves, 3);
    proof[0][0] ^= 0xff;
    assert!(!is_valid_proof(&leaves[3], &proof, &root));
    assert!(!is_valid_proof(
        &filled_hash(99),
        &merkle_proof(&leaves, 3),
        &root
    ));
}

#[test]
fn is_valid_proof_rejects_proofs_longer_than_16() {
    let leaf = filled_hash(1);
    let proof: Vec<[u8; 32]> = (0..17u8).map(filled_hash).collect();
    let mut current = leaf;
    for sibling in &proof {
        current = node_hash(&current, sibling);
    }
    let root = current;
    assert!(
        !is_valid_proof(&leaf, &proof, &root),
        "a 17-sibling proof must be rejected even though it folds to root"
    );
}

#[test]
fn is_valid_proof_accepts_a_legitimate_16_sibling_proof() {
    let count: usize = 1 << 16;
    let leaves: Vec<[u8; 32]> = (0..count)
        .map(|index| [u8::try_from(index % 256).unwrap(); 32])
        .collect();
    let levels = Levels::build(&leaves);
    let root = levels.root();
    for index in [0usize, count / 2, count - 1] {
        let proof = levels.proof(index);
        assert_eq!(proof.len(), 16, "i={index}");
        assert!(is_valid_proof(&leaves[index], &proof, &root), "i={index}");
    }
}
