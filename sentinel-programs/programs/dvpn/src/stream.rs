#[must_use]
pub fn accrued(
    escrow: u64,
    stream_base: u64,
    rate_per_hour: u64,
    stream_started_at: i64,
    now: i64,
) -> u64 {
    dvpn_core::stream::accrued(escrow, stream_base, rate_per_hour, stream_started_at, now)
}

#[must_use]
pub fn checkpoint_if_exhausted(
    escrow: u64,
    stream_base: u64,
    rate_per_hour: u64,
    stream_started_at: i64,
    now: i64,
) -> Option<(u64, i64)> {
    if accrued(escrow, stream_base, rate_per_hour, stream_started_at, now) == escrow {
        Some((escrow, now))
    } else {
        None
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn caps_at_escrow_and_clamps_negative_elapsed() {
        assert_eq!(accrued(100, 0, 10_000_000, 0, 1_000_000), 100);
        assert_eq!(accrued(100, 0, 10_000_000, 100, 50), 0);
    }

    #[test]
    fn refill_only_when_exhausted() {
        assert_eq!(
            checkpoint_if_exhausted(100, 0, 10_000_000, 0, 1_000_000),
            Some((100, 1_000_000))
        );
        assert_eq!(
            checkpoint_if_exhausted(100_000_000, 0, 10_000_000, 0, 90),
            None
        );
    }
}
