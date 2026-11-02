use crate::error::DvpnError;
use anchor_lang::prelude::*;

fn deadline(start: i64, window_seconds: u32) -> Result<i64> {
    start
        .checked_add(i64::from(window_seconds))
        .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))
}

pub fn has_elapsed(start: i64, window_seconds: u32, now: i64) -> Result<bool> {
    Ok(now >= deadline(start, window_seconds)?)
}

pub fn is_live(start: i64, window_seconds: u32, now: i64) -> Result<bool> {
    Ok(now < deadline(start, window_seconds)?)
}

#[cfg(test)]
mod tests {
    #![allow(clippy::unwrap_used)]
    use super::*;

    #[test]
    fn elapsed_boundary_is_inclusive() {
        assert!(has_elapsed(100, 300, 400).unwrap());
        assert!(!has_elapsed(100, 300, 399).unwrap());
    }

    #[test]
    fn overflow_is_typed() {
        assert!(has_elapsed(i64::MAX, 1, 0).is_err());
    }

    #[test]
    fn live_boundary_is_exclusive() {
        assert!(is_live(100, 300, 399).unwrap());
        assert!(!is_live(100, 300, 400).unwrap());
    }
}
