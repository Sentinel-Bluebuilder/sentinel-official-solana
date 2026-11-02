#[must_use]
pub fn accrued(
    escrow: u64,
    stream_base: u64,
    rate_per_hour: u64,
    stream_started_at: i64,
    now: i64,
) -> u64 {
    let elapsed: u64 = now.saturating_sub(stream_started_at).max(0).unsigned_abs();
    let product = u128::from(rate_per_hour).saturating_mul(u128::from(elapsed)) / 3_600u128;
    let raw = u128::from(stream_base).saturating_add(product);
    let capped = raw.min(u128::from(escrow));

    u64::try_from(capped).unwrap_or(escrow)
}

#[cfg(test)]
mod tests {
    #![allow(clippy::unwrap_used, clippy::arithmetic_side_effects, clippy::panic)]
    use super::*;

    const HOUR: i64 = 3_600;
    const DVPN: u64 = 1_000_000;

    #[test]
    fn ninety_seconds_at_ten_per_hour() {
        assert_eq!(accrued(100 * DVPN, 0, 10 * DVPN, 0, 90), 250_000);
    }

    #[test]
    fn capped_at_escrow() {
        assert_eq!(accrued(100 * DVPN, 0, 10 * DVPN, 0, 15 * HOUR), 100 * DVPN);
    }

    #[test]
    fn negative_elapsed_clamps_to_base() {
        assert_eq!(
            accrued(100 * DVPN, 7 * DVPN, 10 * DVPN, 1_000, 900),
            7 * DVPN
        );
    }

    #[test]
    fn max_rate_and_elapsed_saturate_to_escrow() {
        assert_eq!(accrued(u64::MAX, 0, u64::MAX, 0, i64::MAX), u64::MAX);
    }
}
