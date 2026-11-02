use borsh::BorshDeserialize;
use distributor_core::hash::keccak256;
use distributor_vesting::schedule::{DecodeError, Period, Schedule, ScheduleKind};

fn periodic() -> Schedule {
    Schedule {
        kind: ScheduleKind::Periodic,
        start_at: 100,
        end_at: 130,
        periods: vec![
            Period {
                length_seconds: 10,
                amount: 40,
            },
            Period {
                length_seconds: 20,
                amount: 60,
            },
        ],
        total: 100,
    }
}

#[test]
fn preimage_layout_is_pinned_little_endian() {
    let schedule = periodic();
    let mut want = Vec::new();
    want.push(2u8);
    want.extend_from_slice(&100i64.to_le_bytes());
    want.extend_from_slice(&130i64.to_le_bytes());
    want.extend_from_slice(&2u32.to_le_bytes());
    want.extend_from_slice(&10i64.to_le_bytes());
    want.extend_from_slice(&40u64.to_le_bytes());
    want.extend_from_slice(&20i64.to_le_bytes());
    want.extend_from_slice(&60u64.to_le_bytes());
    want.extend_from_slice(&100u64.to_le_bytes());
    assert_eq!(schedule.encode_preimage(), want);
}

#[test]
fn schedule_hash_is_keccak_of_preimage() {
    let schedule = periodic();
    assert_eq!(schedule.hash(), keccak256(&[&schedule.encode_preimage()]));
}

#[test]
fn borsh_round_trips() {
    let schedule = periodic();
    let bytes = borsh::to_vec(&schedule).unwrap();
    assert_eq!(Schedule::try_from_slice(&bytes).unwrap(), schedule);
}

#[test]
fn decode_preimage_round_trips_and_rejects_malformed() {
    let schedule = periodic();
    let bytes = schedule.encode_preimage();
    assert_eq!(Schedule::decode_preimage(&bytes).unwrap(), schedule);
    let mut extra = bytes.clone();
    extra.push(0);
    assert_eq!(
        Schedule::decode_preimage(&extra),
        Err(DecodeError::TrailingBytes)
    );
    assert_eq!(
        Schedule::decode_preimage(&bytes[..bytes.len() - 1]),
        Err(DecodeError::Truncated)
    );
    let mut bad_kind = bytes.clone();
    bad_kind[0] = 3;
    assert_eq!(
        Schedule::decode_preimage(&bad_kind),
        Err(DecodeError::InvalidKind)
    );
}

#[test]
fn decode_preimage_never_panics_on_arbitrary_bytes() {
    for len in 0..80usize {
        for fill in [0u8, 1, 0x7f, 0x80, 0xff] {
            let bytes = vec![fill; len];
            let _ = Schedule::decode_preimage(&bytes);
        }
    }
}

#[cfg(feature = "idl-build")]
#[test]
fn idl_build_emits_type_definitions() {
    use anchor_lang::IdlBuild;
    let def = Schedule::create_type().expect("Schedule must be present in the IDL");
    assert!(def.name.ends_with("::Schedule"), "{}", def.name);
    let mut types = std::collections::BTreeMap::new();
    Schedule::insert_types(&mut types);
    assert!(
        types.contains_key(&ScheduleKind::get_full_path()),
        "ScheduleKind missing"
    );
    assert!(
        types.contains_key(&Period::get_full_path()),
        "Period missing"
    );
}

#[cfg(feature = "serde")]
#[test]
fn serde_encodes_u64_amounts_as_strings() {
    let schedule = Schedule {
        kind: ScheduleKind::Periodic,
        start_at: 0,
        end_at: 10,
        periods: vec![Period {
            length_seconds: 10,
            amount: 18_000_000_000_000_000_000,
        }],
        total: 18_000_000_000_000_000_000,
    };
    let json = serde_json::to_string(&schedule).unwrap();
    assert!(
        json.contains("\"total\":\"18000000000000000000\""),
        "{json}"
    );
    assert!(
        json.contains("\"amount\":\"18000000000000000000\""),
        "{json}"
    );
    let back: Schedule = serde_json::from_str(&json).unwrap();
    assert_eq!(back, schedule);
}
