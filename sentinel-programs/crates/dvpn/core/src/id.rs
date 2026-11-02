#[must_use]
pub fn random_id() -> [u8; 32] {
    let mut buf = [0u8; 32];

    #[allow(clippy::expect_used)]
    getrandom::getrandom(&mut buf).expect("OS CSPRNG unavailable");
    buf
}

#[cfg(test)]
mod tests {
    #![allow(clippy::unwrap_used, clippy::panic)]
    use super::*;

    #[test]
    fn ids_are_distinct_and_nonzero() {
        let first = random_id();
        let second = random_id();
        assert_ne!(first, second);
        assert_ne!(first, [0u8; 32]);
    }
}
