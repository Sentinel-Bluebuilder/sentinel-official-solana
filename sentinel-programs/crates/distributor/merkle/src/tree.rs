use distributor_core::consts::{NODE_TAG, TREE_DOMAIN_SEPARATOR};
use distributor_core::hash::keccak256;

#[must_use]
pub fn node_hash(left: &[u8; 32], right: &[u8; 32]) -> [u8; 32] {
    let (lower, higher) = if left <= right {
        (left, right)
    } else {
        (right, left)
    };
    keccak256(&[&[NODE_TAG], TREE_DOMAIN_SEPARATOR, lower, higher])
}

fn fold_level(level: &[[u8; 32]]) -> Vec<[u8; 32]> {
    let mut next = Vec::with_capacity(level.len().div_ceil(2));
    let mut rest = level;
    while let Some((first, tail)) = rest.split_first() {
        rest = match tail.split_first() {
            Some((second, remainder)) => {
                next.push(node_hash(first, second));
                remainder
            }
            None => {
                next.push(*first);
                tail
            }
        };
    }
    next
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Levels(Vec<Vec<[u8; 32]>>);

impl Levels {
    #[must_use]
    pub fn build(leaves: &[[u8; 32]]) -> Self {
        assert!(!leaves.is_empty(), "Levels::build on empty leaf set");
        let mut levels: Vec<Vec<[u8; 32]>> = Vec::new();
        let mut current = leaves.to_vec();
        while current.len() > 1 {
            let next = fold_level(&current);
            levels.push(current);
            current = next;
        }
        levels.push(current);
        Self(levels)
    }

    #[must_use]
    pub fn leaf_count(&self) -> usize {
        self.0.first().map_or(0, Vec::len)
    }

    #[allow(clippy::expect_used)]
    #[must_use]
    pub fn root(&self) -> [u8; 32] {
        *self
            .0
            .last()
            .and_then(|top| top.first())
            .expect("Levels::build never produces an empty level")
    }

    #[must_use]
    pub fn proof(&self, mut index: usize) -> Vec<[u8; 32]> {
        assert!(index < self.leaf_count(), "index out of range");
        let mut proof = Vec::new();
        for level in &self.0 {
            if level.len() <= 1 {
                break;
            }
            let sibling = if index.is_multiple_of(2) {
                index.checked_add(1)
            } else {
                index.checked_sub(1)
            };
            if let Some(hash) = sibling.and_then(|index| level.get(index)) {
                proof.push(*hash);
            }
            index /= 2;
        }
        proof
    }
}

#[must_use]
pub fn merkle_root(leaves: &[[u8; 32]]) -> [u8; 32] {
    Levels::build(leaves).root()
}

#[must_use]
pub fn merkle_proof(leaves: &[[u8; 32]], index: usize) -> Vec<[u8; 32]> {
    Levels::build(leaves).proof(index)
}

#[must_use]
pub fn is_valid_proof(leaf: &[u8; 32], proof: &[[u8; 32]], root: &[u8; 32]) -> bool {
    if proof.len() > 16 {
        return false;
    }
    let mut current = *leaf;
    for sibling in proof {
        current = node_hash(&current, sibling);
    }
    &current == root
}
