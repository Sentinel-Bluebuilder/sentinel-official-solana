use solana_message::v1::{MAX_HEAP_SIZE, MIN_HEAP_SIZE, TransactionConfig};

#[derive(Clone, Copy, Debug)]
pub struct TxConfig {
    compute_unit_limit: u32,
    loaded_accounts_data_size_limit: u32,
    priority_fee_lamports: u64,
    heap_size_bytes: Option<u32>,
}

#[derive(Debug, thiserror::Error)]
#[error("heap size {0} must be in [32768, 262144] and a multiple of 1024")]
pub struct ConfigError(pub u32);

impl TxConfig {
    #[must_use]
    pub const fn new(compute_unit_limit: u32, loaded_accounts_data_size_limit: u32) -> Self {
        Self {
            compute_unit_limit,
            loaded_accounts_data_size_limit,
            priority_fee_lamports: 0,
            heap_size_bytes: None,
        }
    }

    #[must_use]
    pub fn with_priority_fee_lamports(mut self, lamports: u64) -> Self {
        self.priority_fee_lamports = lamports;
        self
    }

    pub fn with_heap_size(mut self, bytes: u32) -> Result<Self, ConfigError> {
        if !(MIN_HEAP_SIZE..=MAX_HEAP_SIZE).contains(&bytes) || !bytes.is_multiple_of(1024) {
            return Err(ConfigError(bytes));
        }
        self.heap_size_bytes = Some(bytes);
        Ok(self)
    }
}

impl From<&TxConfig> for TransactionConfig {
    fn from(config: &TxConfig) -> Self {
        TransactionConfig {
            priority_fee: (config.priority_fee_lamports != 0)
                .then_some(config.priority_fee_lamports),
            compute_unit_limit: Some(config.compute_unit_limit),
            loaded_accounts_data_size_limit: Some(config.loaded_accounts_data_size_limit),
            heap_size: config.heap_size_bytes,
        }
    }
}

#[cfg(test)]
mod tests {
    #![allow(
        clippy::unwrap_used,
        clippy::expect_used,
        clippy::panic,
        clippy::indexing_slicing,
        clippy::arithmetic_side_effects
    )]
    use super::*;
    use solana_message::v1::TransactionConfig;

    #[test]
    fn config_sets_both_limits_and_omits_zero_priority_fee() {
        let cfg: TransactionConfig = (&TxConfig::new(400_000, 65_536)).into();
        assert_eq!(cfg.compute_unit_limit, Some(400_000));
        assert_eq!(cfg.loaded_accounts_data_size_limit, Some(65_536));
        assert_eq!(cfg.priority_fee, None);
        assert_eq!(cfg.heap_size, None);
    }

    #[test]
    fn heap_size_rejects_out_of_range_and_misaligned() {
        assert!(TxConfig::new(1, 1).with_heap_size(33 * 1024).is_ok());
        assert!(TxConfig::new(1, 1).with_heap_size(33_000).is_err());
        assert!(TxConfig::new(1, 1).with_heap_size(16 * 1024).is_err());
        assert!(TxConfig::new(1, 1).with_heap_size(512 * 1024).is_err());
    }
}
