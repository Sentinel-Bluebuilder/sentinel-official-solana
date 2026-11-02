use num_traits::ToPrimitive;

use crate::legacy_dec::LegacyDec;
use crate::schedule::{Schedule, ScheduleKind};

pub const MAX_PERIOD_COUNT: usize = 512;

pub const MAX_JS_SAFE_INTEGER: i64 = (1i64 << 53) - 1;

#[derive(Clone, Debug, PartialEq, Eq, thiserror::Error)]
#[non_exhaustive]
pub enum VestingError {
    #[error("schedule start is negative")]
    NegativeStart,
    #[error("schedule end must be strictly after its start")]
    EndNotAfterStart,
    #[error("schedule total must be non-zero")]
    ZeroTotal,
    #[error("schedule total does not match the locked amount")]
    TotalMismatch,
    #[error("only a Periodic schedule may carry periods")]
    UnexpectedPeriods,
    #[error("periodic schedule must have between 1 and {MAX_PERIOD_COUNT} periods")]
    PeriodCountMismatch,
    #[error("every period must have a positive length")]
    NonPositiveLength,
    #[error("period amounts do not sum to the schedule total")]
    PeriodAmountSumMismatch,
    #[error("period lengths do not span the schedule window")]
    PeriodLengthSumMismatch,
    #[error("arithmetic overflow while summing periods")]
    PeriodSumOverflow,
    #[error("schedule end exceeds the JS-safe-integer bound")]
    EndAtTooLarge,
    #[error("period length exceeds the JS-safe-integer bound")]
    PeriodLengthTooLarge,
}

#[derive(Clone, Copy, Debug)]
pub struct ValidatedSchedule<'a>(&'a Schedule);

#[must_use]
pub fn unlocked_amount(validated: &ValidatedSchedule, now: i64) -> u64 {
    let schedule = validated.0;
    match schedule.kind {
        ScheduleKind::Delayed => {
            if now >= schedule.end_at {
                schedule.total
            } else {
                0
            }
        }
        #[allow(clippy::arithmetic_side_effects, clippy::expect_used)]
        ScheduleKind::Continuous => {
            if now <= schedule.start_at {
                0
            } else if now >= schedule.end_at {
                schedule.total
            } else {
                let elapsed = now - schedule.start_at;
                let duration_seconds = schedule.end_at - schedule.start_at;
                let scalar =
                    LegacyDec::from_i64(elapsed).quo(&LegacyDec::from_i64(duration_seconds));
                LegacyDec::from_u64(schedule.total)
                    .mul(&scalar)
                    .round_int()
                    .to_u64()
                    .expect("vested is within [0, total] which is a u64")
            }
        }
        #[allow(clippy::arithmetic_side_effects)]
        ScheduleKind::Periodic => {
            if now <= schedule.start_at {
                0
            } else if now >= schedule.end_at {
                schedule.total
            } else {
                let elapsed = now - schedule.start_at;
                let mut cumulative_length_seconds: i64 = 0;
                let mut vested: u64 = 0;
                for period in &schedule.periods {
                    cumulative_length_seconds += period.length_seconds;
                    if cumulative_length_seconds <= elapsed {
                        vested += period.amount;
                    } else {
                        break;
                    }
                }
                vested
            }
        }
    }
}

pub fn check_schedule(
    schedule: &Schedule,
    locked_amount: u64,
) -> Result<ValidatedSchedule<'_>, VestingError> {
    if schedule.start_at < 0 {
        return Err(VestingError::NegativeStart);
    }
    if schedule.end_at <= schedule.start_at {
        return Err(VestingError::EndNotAfterStart);
    }
    if schedule.end_at > MAX_JS_SAFE_INTEGER {
        return Err(VestingError::EndAtTooLarge);
    }
    if schedule.total == 0 {
        return Err(VestingError::ZeroTotal);
    }
    if schedule.total != locked_amount {
        return Err(VestingError::TotalMismatch);
    }
    match schedule.kind {
        ScheduleKind::Continuous | ScheduleKind::Delayed => {
            if !schedule.periods.is_empty() {
                return Err(VestingError::UnexpectedPeriods);
            }
        }
        ScheduleKind::Periodic => {
            if schedule.periods.is_empty() || schedule.periods.len() > MAX_PERIOD_COUNT {
                return Err(VestingError::PeriodCountMismatch);
            }
            let mut sum_amount: u64 = 0;
            let mut sum_length_seconds: i64 = 0;
            for period in &schedule.periods {
                if period.length_seconds <= 0 {
                    return Err(VestingError::NonPositiveLength);
                }
                if period.length_seconds > MAX_JS_SAFE_INTEGER {
                    return Err(VestingError::PeriodLengthTooLarge);
                }
                sum_amount = sum_amount
                    .checked_add(period.amount)
                    .ok_or(VestingError::PeriodSumOverflow)?;
                sum_length_seconds = sum_length_seconds
                    .checked_add(period.length_seconds)
                    .ok_or(VestingError::PeriodSumOverflow)?;
            }
            if sum_amount != schedule.total {
                return Err(VestingError::PeriodAmountSumMismatch);
            }
            if schedule.start_at.checked_add(sum_length_seconds) != Some(schedule.end_at) {
                return Err(VestingError::PeriodLengthSumMismatch);
            }
        }
    }
    Ok(ValidatedSchedule(schedule))
}
