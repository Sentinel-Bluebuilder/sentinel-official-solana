#[must_use]
pub fn priority_fee_lamports(micro_lamports_per_compute_unit: u64, compute_unit_limit: u32) -> u64 {
    let micro =
        u128::from(micro_lamports_per_compute_unit).saturating_mul(u128::from(compute_unit_limit));
    u64::try_from(micro.div_ceil(1_000_000)).unwrap_or(u64::MAX)
}
