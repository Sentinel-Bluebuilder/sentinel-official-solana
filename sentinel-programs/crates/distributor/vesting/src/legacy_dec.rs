#![allow(clippy::arithmetic_side_effects)]

use num_bigint::{BigInt, Sign};
use num_integer::Integer;

fn precision() -> BigInt {
    BigInt::from(1_000_000_000_000_000_000i64)
}

fn five_precision() -> BigInt {
    BigInt::from(500_000_000_000_000_000i64)
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct LegacyDec(BigInt);

impl LegacyDec {
    #[must_use]
    pub fn from_i64(value: i64) -> LegacyDec {
        LegacyDec(BigInt::from(value) * precision())
    }

    #[must_use]
    pub fn from_u64(value: u64) -> LegacyDec {
        LegacyDec(BigInt::from(value) * precision())
    }

    #[must_use]
    pub fn quo(&self, other: &LegacyDec) -> LegacyDec {
        let precision_scale = precision();
        let mut mul = &self.0 * &precision_scale;
        mul *= &precision_scale;
        let quo = &mul / &other.0;
        LegacyDec(chop_precision_and_round(&quo))
    }

    #[must_use]
    pub fn mul(&self, other: &LegacyDec) -> LegacyDec {
        let prod = &self.0 * &other.0;
        LegacyDec(chop_precision_and_round(&prod))
    }

    #[must_use]
    pub fn round_int(&self) -> BigInt {
        chop_precision_and_round(&self.0)
    }
}

fn chop_precision_and_round(dec: &BigInt) -> BigInt {
    if dec.sign() == Sign::Minus {
        return -chop_precision_and_round(&-dec);
    }
    let (quo, rem) = dec.div_rem(&precision());
    if rem.sign() == Sign::NoSign {
        return quo;
    }
    match rem.cmp(&five_precision()) {
        std::cmp::Ordering::Less => quo,
        std::cmp::Ordering::Greater => quo + 1,
        std::cmp::Ordering::Equal => {
            if quo.is_even() {
                quo
            } else {
                quo + 1
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[allow(clippy::unwrap_used)]
    fn big_int(digits: &str) -> BigInt {
        BigInt::parse_bytes(digits.as_bytes(), 10).unwrap()
    }

    #[test]
    fn quo_rounds_banker() {
        assert_eq!(
            LegacyDec::from_i64(1).quo(&LegacyDec::from_i64(3)).0,
            big_int("333333333333333333")
        );
        assert_eq!(
            LegacyDec::from_i64(2).quo(&LegacyDec::from_i64(3)).0,
            big_int("666666666666666667")
        );
    }

    #[test]
    fn mul_then_round_int() {
        let third = LegacyDec::from_i64(1).quo(&LegacyDec::from_i64(3));
        let product = LegacyDec::from_u64(1000).mul(&third);
        assert_eq!(product.0, big_int("333333333333333333000"));
        assert_eq!(product.round_int(), big_int("333"));
    }

    #[test]
    fn round_int_is_banker_half_to_even() {
        assert_eq!(
            LegacyDec(big_int("500000000000000000")).round_int(),
            big_int("0")
        );
        assert_eq!(
            LegacyDec(big_int("1500000000000000000")).round_int(),
            big_int("2")
        );
        assert_eq!(
            LegacyDec(big_int("2500000000000000000")).round_int(),
            big_int("2")
        );
        assert_eq!(
            LegacyDec(big_int("3500000000000000000")).round_int(),
            big_int("4")
        );
        assert_eq!(
            LegacyDec(big_int("2500000000000000001")).round_int(),
            big_int("3")
        );
        assert_eq!(
            LegacyDec(big_int("2499999999999999999")).round_int(),
            big_int("2")
        );
    }
}
