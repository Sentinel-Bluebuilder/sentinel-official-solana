use distributor_core::address::{decode_bech32_address, encode_bech32_address};
use distributor_core::hash::keccak256;
use distributor_vesting::schedule::Schedule;

const MAGIC: &[u8; 8] = b"SENTLEDG";
const FORMAT_VERSION: u32 = 1;
const MIN_RECORD_BYTES: usize = 4 + 1 + 8 + 8 + 1;

#[derive(Clone)]
pub struct ParsedRecord {
    pub bech32_address: String,
    pub compressed_public_key: Option<[u8; 33]>,
    pub liquid_amount: u64,
    pub locked_amount: u64,
    pub schedule: Option<Schedule>,
}

#[derive(Debug, PartialEq, Eq)]
pub enum LedgerError {
    InvalidMagic,
    UnsupportedVersion,
    Truncated,
    ContentHashMismatch,
    InvalidUtf8,
    InvalidTag,
    InvalidSchedule,
    LockedScheduleMismatch,
    ZeroEntitlement,
    AddressesUnsorted,
    TrailingBytes,
    InvalidAddress,
}

struct Cursor<'a> {
    bytes: &'a [u8],
    offset: usize,
}

impl<'a> Cursor<'a> {
    fn take(&mut self, total: usize) -> Result<&'a [u8], LedgerError> {
        let end = self
            .offset
            .checked_add(total)
            .ok_or(LedgerError::Truncated)?;
        let slice = self
            .bytes
            .get(self.offset..end)
            .ok_or(LedgerError::Truncated)?;
        self.offset = end;
        Ok(slice)
    }
    fn take_u8(&mut self) -> Result<u8, LedgerError> {
        self.take(1)?.first().copied().ok_or(LedgerError::Truncated)
    }
    fn peek_u32(&self, offset: usize) -> Result<u32, LedgerError> {
        let start = self
            .offset
            .checked_add(offset)
            .ok_or(LedgerError::Truncated)?;
        let end = start.checked_add(4).ok_or(LedgerError::Truncated)?;
        let slice = self.bytes.get(start..end).ok_or(LedgerError::Truncated)?;
        Ok(u32::from_le_bytes(slice.try_into().unwrap()))
    }
}

pub fn decode_ledger(bytes: &[u8]) -> Result<Vec<ParsedRecord>, LedgerError> {
    if bytes.len() < 8 + 4 + 8 + 32 {
        return Err(LedgerError::Truncated);
    }

    #[allow(clippy::indexing_slicing)]
    if &bytes[0..8] != MAGIC {
        return Err(LedgerError::InvalidMagic);
    }
    #[allow(clippy::indexing_slicing)]
    if u32::from_le_bytes(bytes[8..12].try_into().unwrap()) != FORMAT_VERSION {
        return Err(LedgerError::UnsupportedVersion);
    }
    #[allow(clippy::indexing_slicing)]
    let count = usize::try_from(u64::from_le_bytes(bytes[12..20].try_into().unwrap()))
        .map_err(|_| LedgerError::Truncated)?;
    #[allow(clippy::arithmetic_side_effects)]
    let (body, stored) = bytes.split_at(bytes.len() - 32);
    if keccak256(&[body]) != stored {
        return Err(LedgerError::ContentHashMismatch);
    }

    #[allow(clippy::arithmetic_side_effects)]
    let cap = count.min((body.len() - 20) / MIN_RECORD_BYTES + 1);
    let mut records = Vec::with_capacity(cap);
    let mut cursor = Cursor {
        bytes: body,
        offset: 20,
    };
    let mut previous_address: Option<[u8; 20]> = None;

    for _ in 0..count {
        let address_len = u32::from_le_bytes(cursor.take(4)?.try_into().unwrap()) as usize;
        let address = std::str::from_utf8(cursor.take(address_len)?)
            .map_err(|_| LedgerError::InvalidUtf8)?
            .to_string();

        let address_bytes =
            decode_bech32_address(&address).map_err(|_| LedgerError::InvalidAddress)?;
        if let Some(prev) = previous_address
            && address_bytes <= prev
        {
            return Err(LedgerError::AddressesUnsorted);
        }
        previous_address = Some(address_bytes);

        let compressed_public_key = match cursor.take_u8()? {
            0 => None,
            1 => Some(<[u8; 33]>::try_from(cursor.take(33)?).unwrap()),
            _ => return Err(LedgerError::InvalidTag),
        };
        let liquid = u64::from_le_bytes(cursor.take(8)?.try_into().unwrap());
        let locked = u64::from_le_bytes(cursor.take(8)?.try_into().unwrap());
        let schedule = match cursor.take_u8()? {
            0 => None,
            1 => {
                let total = cursor.peek_u32(17)? as usize;
                let schedule_len = total
                    .checked_mul(16)
                    .and_then(|periods_len| 29usize.checked_add(periods_len))
                    .ok_or(LedgerError::InvalidSchedule)?;
                let schedule = Schedule::decode_preimage(cursor.take(schedule_len)?)
                    .map_err(|_| LedgerError::InvalidSchedule)?;
                Some(schedule)
            }
            _ => return Err(LedgerError::InvalidTag),
        };
        if (locked > 0) != schedule.is_some() {
            return Err(LedgerError::LockedScheduleMismatch);
        }
        if liquid == 0 && locked == 0 {
            return Err(LedgerError::ZeroEntitlement);
        }
        records.push(ParsedRecord {
            bech32_address: address,
            compressed_public_key,
            liquid_amount: liquid,
            locked_amount: locked,
            schedule,
        });
    }
    if cursor.offset != body.len() {
        return Err(LedgerError::TrailingBytes);
    }
    Ok(records)
}

#[must_use]
pub fn encode_ledger(records: &[ParsedRecord]) -> Vec<u8> {
    let mut sorted: Vec<&ParsedRecord> = records.iter().collect();
    sorted.sort_by_cached_key(|record| {
        decode_bech32_address(&record.bech32_address).expect("valid address")
    });

    let mut body = Vec::new();
    body.extend_from_slice(MAGIC);
    body.extend_from_slice(&FORMAT_VERSION.to_le_bytes());
    body.extend_from_slice(&(sorted.len() as u64).to_le_bytes());
    for record in &sorted {
        debug_assert_eq!(
            encode_bech32_address(&decode_bech32_address(&record.bech32_address).unwrap()),
            record.bech32_address
        );
        let address = record.bech32_address.as_bytes();
        let address_len = u32::try_from(address.len()).expect("bech32 address length fits u32");
        body.extend_from_slice(&address_len.to_le_bytes());
        body.extend_from_slice(address);
        match &record.compressed_public_key {
            Some(compressed_public_key) => {
                body.push(1);
                body.extend_from_slice(compressed_public_key);
            }
            None => body.push(0),
        }
        body.extend_from_slice(&record.liquid_amount.to_le_bytes());
        body.extend_from_slice(&record.locked_amount.to_le_bytes());
        match &record.schedule {
            Some(schedule) => {
                body.push(1);
                body.extend_from_slice(&schedule.encode_preimage());
            }
            None => body.push(0),
        }
    }
    let hash = keccak256(&[&body]);
    body.extend_from_slice(&hash);
    body
}
