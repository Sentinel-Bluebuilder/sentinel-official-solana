pub const PERM_OPEN_NODE: u64 = 1 << 0;
pub const PERM_UPDATE_NODE: u64 = 1 << 1;
pub const PERM_LIST_NODE: u64 = 1 << 2;
pub const PERM_DELIST_NODE: u64 = 1 << 3;
pub const PERM_CLOSE_NODE: u64 = 1 << 4;
pub const PERM_WITHDRAW_NODE_BOND: u64 = 1 << 5;
pub const PERM_WITHDRAW_NODE_REVENUE: u64 = 1 << 6;
pub const PERM_OPEN_PROVIDER: u64 = 1 << 7;
pub const PERM_UPDATE_PROVIDER: u64 = 1 << 8;
pub const PERM_LIST_PROVIDER: u64 = 1 << 9;
pub const PERM_DELIST_PROVIDER: u64 = 1 << 10;
pub const PERM_CLOSE_PROVIDER: u64 = 1 << 11;
pub const PERM_WITHDRAW_PROVIDER_BOND: u64 = 1 << 12;
pub const PERM_WITHDRAW_PROVIDER_REVENUE: u64 = 1 << 13;
pub const PERM_WITHDRAW_PROVIDER_BALANCE: u64 = 1 << 14;
pub const PERM_OPEN_PLAN: u64 = 1 << 15;
pub const PERM_UPDATE_PLAN: u64 = 1 << 16;
pub const PERM_LIST_PLAN: u64 = 1 << 17;
pub const PERM_DELIST_PLAN: u64 = 1 << 18;
pub const PERM_CLOSE_PLAN: u64 = 1 << 19;
pub const PERM_OPEN_LEASE: u64 = 1 << 20;
pub const PERM_FUND_LEASES: u64 = 1 << 21;
pub const PERM_START_CLOSE_LEASE: u64 = 1 << 22;
pub const PERM_CLOSE_LEASE: u64 = 1 << 23;
pub const PERM_OPEN_CHANNEL: u64 = 1 << 24;
pub const PERM_FUND_CHANNELS: u64 = 1 << 25;
pub const PERM_START_CLOSE_CHANNEL: u64 = 1 << 26;
pub const PERM_CLOSE_CHANNEL: u64 = 1 << 27;
pub const PERM_WITHDRAW_USER_BALANCE: u64 = 1 << 28;
pub const PERM_OPEN_SUBSCRIPTION: u64 = 1 << 29;
pub const PERM_CLOSE_SUBSCRIPTION: u64 = 1 << 30;
pub const PERM_OPEN_FREE_SUBSCRIPTION: u64 = 1 << 31;
pub const PERM_CLOSE_FREE_SUBSCRIPTION: u64 = 1 << 32;

pub const PERM_COUNT: u32 = 33;

pub const PERM_ALL: u64 = (1 << PERM_COUNT) - 1;

#[must_use]
pub fn is_valid_permissions(permissions: u64) -> bool {
    permissions != 0 && permissions & !PERM_ALL == 0
}

#[cfg(test)]
mod tests {
    use super::*;

    const EVERY_BIT: [u64; 33] = [
        PERM_OPEN_NODE,
        PERM_UPDATE_NODE,
        PERM_LIST_NODE,
        PERM_DELIST_NODE,
        PERM_CLOSE_NODE,
        PERM_WITHDRAW_NODE_BOND,
        PERM_WITHDRAW_NODE_REVENUE,
        PERM_OPEN_PROVIDER,
        PERM_UPDATE_PROVIDER,
        PERM_LIST_PROVIDER,
        PERM_DELIST_PROVIDER,
        PERM_CLOSE_PROVIDER,
        PERM_WITHDRAW_PROVIDER_BOND,
        PERM_WITHDRAW_PROVIDER_REVENUE,
        PERM_WITHDRAW_PROVIDER_BALANCE,
        PERM_OPEN_PLAN,
        PERM_UPDATE_PLAN,
        PERM_LIST_PLAN,
        PERM_DELIST_PLAN,
        PERM_CLOSE_PLAN,
        PERM_OPEN_LEASE,
        PERM_FUND_LEASES,
        PERM_START_CLOSE_LEASE,
        PERM_CLOSE_LEASE,
        PERM_OPEN_CHANNEL,
        PERM_FUND_CHANNELS,
        PERM_START_CLOSE_CHANNEL,
        PERM_CLOSE_CHANNEL,
        PERM_WITHDRAW_USER_BALANCE,
        PERM_OPEN_SUBSCRIPTION,
        PERM_CLOSE_SUBSCRIPTION,
        PERM_OPEN_FREE_SUBSCRIPTION,
        PERM_CLOSE_FREE_SUBSCRIPTION,
    ];

    #[test]
    fn every_bit_is_distinct_and_covered_by_all() {
        let mut seen = 0u64;
        for bit in EVERY_BIT {
            assert_eq!(bit.count_ones(), 1);
            assert_eq!(seen & bit, 0);
            seen |= bit;
        }
        assert_eq!(seen, PERM_ALL);
        assert_eq!(PERM_ALL.count_ones(), PERM_COUNT);
    }

    #[test]
    fn validity_rejects_zero_and_unknown_bits() {
        assert!(is_valid_permissions(PERM_OPEN_NODE));
        assert!(is_valid_permissions(PERM_ALL));
        assert!(!is_valid_permissions(0));
        assert!(!is_valid_permissions(1 << 33));
        assert!(!is_valid_permissions(PERM_ALL | 1 << 63));
    }
}
