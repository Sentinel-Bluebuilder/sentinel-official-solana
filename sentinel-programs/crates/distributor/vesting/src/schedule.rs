use borsh::{BorshDeserialize, BorshSerialize};
use distributor_core::hash::keccak256;

pub const ZERO_SCHEDULE_HASH: [u8; 32] = [0u8; 32];

#[cfg(feature = "serde")]
mod u64_str {
    #[allow(clippy::trivially_copy_pass_by_ref)]
    pub(super) fn serialize<S: serde::Serializer>(
        value: &u64,
        serializer: S,
    ) -> Result<S::Ok, S::Error> {
        serializer.serialize_str(&value.to_string())
    }
    pub(super) fn deserialize<'de, D: serde::Deserializer<'de>>(
        deserializer: D,
    ) -> Result<u64, D::Error> {
        let text = <String as serde::Deserialize>::deserialize(deserializer)?;
        text.parse().map_err(serde::de::Error::custom)
    }
}

#[derive(BorshSerialize, BorshDeserialize, Clone, Copy, PartialEq, Eq, Debug)]
#[cfg_attr(feature = "serde", derive(serde::Serialize, serde::Deserialize))]
pub enum ScheduleKind {
    Continuous,
    Delayed,
    Periodic,
}

impl ScheduleKind {
    #[must_use]
    pub fn tag(self) -> u8 {
        match self {
            ScheduleKind::Continuous => 0,
            ScheduleKind::Delayed => 1,
            ScheduleKind::Periodic => 2,
        }
    }
}

#[derive(BorshSerialize, BorshDeserialize, Clone, Copy, PartialEq, Eq, Debug)]
#[cfg_attr(feature = "serde", derive(serde::Serialize, serde::Deserialize))]
pub struct Period {
    pub length_seconds: i64,
    #[cfg_attr(feature = "serde", serde(with = "u64_str"))]
    pub amount: u64,
}

pub const PERIOD_ENCODED_BYTES: usize = 16;

impl From<Period> for [u8; PERIOD_ENCODED_BYTES] {
    fn from(period: Period) -> [u8; PERIOD_ENCODED_BYTES] {
        let mut bytes = [0u8; PERIOD_ENCODED_BYTES];
        bytes[0..8].copy_from_slice(&period.length_seconds.to_le_bytes());
        bytes[8..16].copy_from_slice(&period.amount.to_le_bytes());
        bytes
    }
}

impl From<[u8; PERIOD_ENCODED_BYTES]> for Period {
    fn from(bytes: [u8; PERIOD_ENCODED_BYTES]) -> Period {
        let mut length_seconds = [0u8; 8];
        let mut amount = [0u8; 8];
        length_seconds.copy_from_slice(&bytes[0..8]);
        amount.copy_from_slice(&bytes[8..16]);
        Period {
            length_seconds: i64::from_le_bytes(length_seconds),
            amount: u64::from_le_bytes(amount),
        }
    }
}

impl Period {
    #[must_use]
    pub fn to_le_bytes(self) -> [u8; PERIOD_ENCODED_BYTES] {
        self.into()
    }

    #[must_use]
    pub fn from_le_bytes(bytes: &[u8; PERIOD_ENCODED_BYTES]) -> Period {
        (*bytes).into()
    }
}

#[derive(BorshSerialize, BorshDeserialize, Clone, PartialEq, Eq, Debug)]
#[cfg_attr(feature = "serde", derive(serde::Serialize, serde::Deserialize))]
pub struct Schedule {
    pub kind: ScheduleKind,
    pub start_at: i64,
    pub end_at: i64,
    pub periods: Vec<Period>,
    #[cfg_attr(feature = "serde", serde(with = "u64_str"))]
    pub total: u64,
}

#[cfg(feature = "idl-build")]
use anchor_lang::IdlBuild;
#[cfg(feature = "idl-build")]
use anchor_lang::idl::types::{
    IdlDefinedFields, IdlEnumVariant, IdlField, IdlSerialization, IdlType, IdlTypeDef, IdlTypeDefTy,
};

#[cfg(feature = "idl-build")]
fn idl_type_def(name: String, ty: IdlTypeDefTy) -> IdlTypeDef {
    IdlTypeDef {
        name,
        docs: vec![],
        serialization: IdlSerialization::Borsh,
        repr: None,
        generics: vec![],
        ty,
    }
}

#[cfg(feature = "idl-build")]
fn idl_field(name: &str, docs: &[&str], ty: IdlType) -> IdlField {
    IdlField {
        name: name.into(),
        docs: docs.iter().map(|doc| (*doc).into()).collect(),
        ty,
    }
}

#[cfg(feature = "idl-build")]
impl IdlBuild for ScheduleKind {
    fn create_type() -> Option<IdlTypeDef> {
        Some(idl_type_def(
            Self::get_full_path(),
            IdlTypeDefTy::Enum {
                variants: ["Continuous", "Delayed", "Periodic"]
                    .into_iter()
                    .map(|name| IdlEnumVariant {
                        name: name.into(),
                        fields: None,
                    })
                    .collect(),
            },
        ))
    }

    fn get_full_path() -> String {
        format!("{}::ScheduleKind", module_path!())
    }
}

#[cfg(feature = "idl-build")]
impl IdlBuild for Period {
    fn create_type() -> Option<IdlTypeDef> {
        Some(idl_type_def(
            Self::get_full_path(),
            IdlTypeDefTy::Struct {
                fields: Some(IdlDefinedFields::Named(vec![
                    idl_field(
                        "length_seconds",
                        &["Length of the period in seconds."],
                        IdlType::I64,
                    ),
                    idl_field("amount", &[], IdlType::U64),
                ])),
            },
        ))
    }

    fn get_full_path() -> String {
        format!("{}::Period", module_path!())
    }
}

#[cfg(feature = "idl-build")]
impl IdlBuild for Schedule {
    fn create_type() -> Option<IdlTypeDef> {
        Some(idl_type_def(
            Self::get_full_path(),
            IdlTypeDefTy::Struct {
                fields: Some(IdlDefinedFields::Named(vec![
                    idl_field(
                        "kind",
                        &[],
                        IdlType::Defined {
                            name: <ScheduleKind>::get_full_path(),
                            generics: vec![],
                        },
                    ),
                    idl_field("start_at", &["Unix seconds."], IdlType::I64),
                    idl_field("end_at", &["Unix seconds."], IdlType::I64),
                    idl_field(
                        "periods",
                        &[],
                        IdlType::Vec(Box::new(IdlType::Defined {
                            name: <Period>::get_full_path(),
                            generics: vec![],
                        })),
                    ),
                    idl_field("total", &[], IdlType::U64),
                ])),
            },
        ))
    }

    fn insert_types(types: &mut std::collections::BTreeMap<String, IdlTypeDef>) {
        for (path, ty) in [
            (
                <ScheduleKind>::get_full_path(),
                <ScheduleKind>::create_type(),
            ),
            (<Period>::get_full_path(), <Period>::create_type()),
        ] {
            if let Some(ty) = ty {
                types.insert(path, ty);
            }
        }
    }

    fn get_full_path() -> String {
        format!("{}::Schedule", module_path!())
    }
}

#[derive(Clone, Debug, PartialEq, Eq, thiserror::Error)]
#[non_exhaustive]
pub enum DecodeError {
    #[error("schedule preimage truncated")]
    Truncated,
    #[error("invalid schedule kind tag")]
    InvalidKind,
    #[error("trailing bytes after schedule preimage")]
    TrailingBytes,
}

impl Schedule {
    #[must_use]
    pub fn encode_preimage(&self) -> Vec<u8> {
        let mut buf = Vec::with_capacity(
            self.periods
                .len()
                .saturating_mul(PERIOD_ENCODED_BYTES)
                .saturating_add(1 + 8 + 8 + 4 + 8),
        );
        buf.push(self.kind.tag());
        buf.extend_from_slice(&self.start_at.to_le_bytes());
        buf.extend_from_slice(&self.end_at.to_le_bytes());
        let count = u32::try_from(self.periods.len()).unwrap_or(u32::MAX);
        buf.extend_from_slice(&count.to_le_bytes());
        for period in &self.periods {
            buf.extend_from_slice(&period.to_le_bytes());
        }
        buf.extend_from_slice(&self.total.to_le_bytes());
        buf
    }

    #[must_use]
    pub fn hash(&self) -> [u8; 32] {
        keccak256(&[&self.encode_preimage()])
    }

    pub fn decode_preimage(preimage: &[u8]) -> Result<Schedule, DecodeError> {
        let mut cursor = Cursor {
            bytes: preimage,
            offset: 0,
        };
        let kind = match cursor.take::<1>()?[0] {
            0 => ScheduleKind::Continuous,
            1 => ScheduleKind::Delayed,
            2 => ScheduleKind::Periodic,
            _ => return Err(DecodeError::InvalidKind),
        };
        let start_at = i64::from_le_bytes(cursor.take::<8>()?);
        let end_at = i64::from_le_bytes(cursor.take::<8>()?);
        let count = u32::from_le_bytes(cursor.take::<4>()?) as usize;
        let max_possible = preimage.len().saturating_sub(21) / 16;
        let mut periods = Vec::with_capacity(count.min(max_possible));
        for _ in 0..count {
            let length_seconds = i64::from_le_bytes(cursor.take::<8>()?);
            let amount = u64::from_le_bytes(cursor.take::<8>()?);
            periods.push(Period {
                length_seconds,
                amount,
            });
        }
        let total = u64::from_le_bytes(cursor.take::<8>()?);
        if cursor.offset != preimage.len() {
            return Err(DecodeError::TrailingBytes);
        }
        Ok(Schedule {
            kind,
            start_at,
            end_at,
            periods,
            total,
        })
    }
}

struct Cursor<'a> {
    bytes: &'a [u8],
    offset: usize,
}

impl<'a> Cursor<'a> {
    fn take<const N: usize>(&mut self) -> Result<[u8; N], DecodeError> {
        let end = self.offset.checked_add(N).ok_or(DecodeError::Truncated)?;
        let taken = self
            .bytes
            .get(self.offset..end)
            .ok_or(DecodeError::Truncated)?;
        self.offset = end;
        #[allow(clippy::expect_used)]
        Ok(taken.try_into().expect("get() yielded exactly N bytes"))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn period_le_roundtrip_and_len() {
        assert_eq!(PERIOD_ENCODED_BYTES, 16);
        let period = Period {
            length_seconds: -123_456,
            amount: 987_654_321,
        };
        let bytes = period.to_le_bytes();
        assert_eq!(bytes.len(), PERIOD_ENCODED_BYTES);
        assert_eq!(Period::from_le_bytes(&bytes), period);
        assert_eq!(<[u8; PERIOD_ENCODED_BYTES]>::from(period), bytes);
        assert_eq!(Period::from(bytes), period);
    }
}
