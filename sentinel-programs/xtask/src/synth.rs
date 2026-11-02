use distributor_vesting::schedule::{Schedule, ScheduleKind};
use k256::ecdsa::SigningKey;
use sha2::{Digest, Sha256};

use crate::ledger::ParsedRecord;

pub struct SynthRecord {
    pub record: ParsedRecord,
    pub cosmos_seed: u8,
}

fn compressed_public_key(seed: u8) -> [u8; 33] {
    let hash = Sha256::digest([seed]);
    let signing_key = SigningKey::from_slice(&hash).expect("seed hash is a valid secp256k1 scalar");
    let mut out = [0u8; 33];
    out.copy_from_slice(signing_key.verifying_key().to_sec1_point(true).as_bytes());
    out
}

fn bech32_address_for(public_key: &[u8; 33]) -> String {
    distributor_core::address::encode_bech32_address(&distributor_core::hash::hash160(public_key))
}

fn revealed(seed: u8, liquid: u64) -> SynthRecord {
    let public_key = compressed_public_key(seed);
    SynthRecord {
        record: ParsedRecord {
            bech32_address: bech32_address_for(&public_key),
            compressed_public_key: Some(public_key),
            liquid_amount: liquid,
            locked_amount: 0,
            schedule: None,
        },
        cosmos_seed: seed,
    }
}

fn unrevealed(seed: u8, liquid: u64) -> SynthRecord {
    let public_key = compressed_public_key(seed);
    SynthRecord {
        record: ParsedRecord {
            bech32_address: bech32_address_for(&public_key),
            compressed_public_key: None,
            liquid_amount: liquid,
            locked_amount: 0,
            schedule: None,
        },
        cosmos_seed: seed,
    }
}

fn locked_continuous(
    seed: u8,
    liquid: u64,
    locked: u64,
    start_at: i64,
    end_at: i64,
) -> SynthRecord {
    let public_key = compressed_public_key(seed);
    let schedule = Schedule {
        kind: ScheduleKind::Continuous,
        start_at,
        end_at,
        periods: vec![],
        total: locked,
    };
    SynthRecord {
        record: ParsedRecord {
            bech32_address: bech32_address_for(&public_key),
            compressed_public_key: Some(public_key),
            liquid_amount: liquid,
            locked_amount: locked,
            schedule: Some(schedule),
        },
        cosmos_seed: seed,
    }
}

const FAR_FUTURE_START_UNIX: i64 = 4_000_000_000;
const FAR_FUTURE_END_UNIX: i64 = FAR_FUTURE_START_UNIX + 31_536_000;

pub fn records(count: usize) -> Result<Vec<SynthRecord>, String> {
    if !(3..=255).contains(&count) {
        return Err(format!(
            "records: count must be in 3..=255 (one u8 seed byte per record), got {count}"
        ));
    }
    let mut out = Vec::with_capacity(count);
    out.push(unrevealed(1, 101));
    out.push(locked_continuous(
        2,
        50,
        1_000_000,
        FAR_FUTURE_START_UNIX,
        FAR_FUTURE_END_UNIX,
    ));

    let last_seed = u8::try_from(count).expect("count <= 255, checked above");
    for seed in 3..=last_seed {
        let amount = u64::from(seed)
            .checked_add(100)
            .expect("seed <= 255, cannot overflow u64");
        out.push(revealed(seed, amount));
    }
    Ok(out)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn synth_mix_has_revealed_unrevealed_and_locked() {
        let recs = records(10).unwrap();
        assert_eq!(recs.len(), 10);
        assert!(
            recs.iter()
                .any(|synthetic| synthetic.record.compressed_public_key.is_none())
        );
        assert!(
            recs.iter()
                .any(|synthetic| synthetic.record.compressed_public_key.is_some()
                    && synthetic.record.locked_amount == 0)
        );
        assert!(
            recs.iter()
                .any(|synthetic| synthetic.record.locked_amount > 0
                    && synthetic.record.schedule.is_some())
        );
        for synthetic in &recs {
            assert!(synthetic.record.liquid_amount + synthetic.record.locked_amount > 0);
            if let Some(schedule) = &synthetic.record.schedule {
                distributor_vesting::unlock::check_schedule(
                    schedule,
                    synthetic.record.locked_amount,
                )
                .expect("valid schedule");
            }
        }
    }

    #[test]
    fn synth_public_key_matches_cosmos_key_from_seed() {
        for synthetic in records(10).unwrap() {
            let expected = distributor_test_support::CosmosKey::from_seed(synthetic.cosmos_seed)
                .compressed_public_key();
            match synthetic.record.compressed_public_key {
                Some(public_key) => assert_eq!(public_key, expected),
                None => assert_eq!(compressed_public_key(synthetic.cosmos_seed), expected),
            }
        }
    }

    #[test]
    fn records_rejects_out_of_range_count() {
        assert!(
            records(2).is_err(),
            "count < 3 must be an error, not a panic"
        );
        assert!(records(256).is_err(), "count > 255 must be an error");
        assert!(records(3).is_ok());
    }
}
