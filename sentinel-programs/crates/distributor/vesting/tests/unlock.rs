use distributor_vesting::schedule::{Period, Schedule, ScheduleKind};
use distributor_vesting::unlock::{VestingError, check_schedule, unlocked_amount};

fn continuous(total: u64, start: i64, end: i64) -> Schedule {
    Schedule {
        kind: ScheduleKind::Continuous,
        start_at: start,
        end_at: end,
        periods: vec![],
        total,
    }
}

fn delayed(total: u64, end: i64) -> Schedule {
    Schedule {
        kind: ScheduleKind::Delayed,
        start_at: 0,
        end_at: end,
        periods: vec![],
        total,
    }
}

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
fn delayed_is_all_or_nothing_at_end() {
    let schedule = delayed(1000, 5);
    let validated = check_schedule(&schedule, 1000).expect("schedule is valid");
    assert_eq!(unlocked_amount(&validated, 4), 0);
    assert_eq!(unlocked_amount(&validated, 5), 1000);
    assert_eq!(unlocked_amount(&validated, 6), 1000);
}

#[test]
fn continuous_boundaries_and_rounding() {
    let schedule = continuous(1000, 0, 3);
    let validated = check_schedule(&schedule, 1000).expect("schedule is valid");
    assert_eq!(unlocked_amount(&validated, -1), 0);
    assert_eq!(unlocked_amount(&validated, 0), 0);
    assert_eq!(unlocked_amount(&validated, 1), 333);
    assert_eq!(unlocked_amount(&validated, 2), 667);
    assert_eq!(unlocked_amount(&validated, 3), 1000);
    assert_eq!(unlocked_amount(&validated, 4), 1000);
}

#[test]
fn continuous_realistic_magnitudes() {
    let schedule = continuous(1_000_000, 1000, 5000);
    let validated = check_schedule(&schedule, 1_000_000).expect("schedule is valid");
    assert_eq!(unlocked_amount(&validated, 2000), 250_000);
    let schedule = continuous(7, 0, 3);
    let validated = check_schedule(&schedule, 7).expect("schedule is valid");
    assert_eq!(unlocked_amount(&validated, 1), 2);
    assert_eq!(unlocked_amount(&validated, 2), 5);
    let schedule = continuous(1_000_000_000, 0, 7);
    let validated = check_schedule(&schedule, 1_000_000_000).expect("schedule is valid");
    assert_eq!(unlocked_amount(&validated, 3), 428_571_429);
    assert_eq!(unlocked_amount(&validated, 1), 142_857_143);
}

#[test]
fn periodic_accumulates_completed_periods() {
    let schedule = periodic();
    let validated = check_schedule(&schedule, 100).expect("schedule is valid");
    assert_eq!(unlocked_amount(&validated, 100), 0);
    assert_eq!(unlocked_amount(&validated, 109), 0);
    assert_eq!(unlocked_amount(&validated, 110), 40);
    assert_eq!(unlocked_amount(&validated, 129), 40);
    assert_eq!(unlocked_amount(&validated, 130), 100);
    assert_eq!(unlocked_amount(&validated, 131), 100);
}

#[test]
fn check_schedule_accepts_well_formed() {
    assert!(check_schedule(&continuous(1000, 0, 3), 1000).is_ok());
    assert!(check_schedule(&delayed(1000, 5), 1000).is_ok());
    assert!(check_schedule(&periodic(), 100).is_ok());
}

#[test]
fn check_schedule_rejects_common_violations() {
    assert_eq!(
        check_schedule(&continuous(1, 5, 5), 1).unwrap_err(),
        VestingError::EndNotAfterStart
    );
    assert_eq!(
        check_schedule(&continuous(0, 0, 3), 0).unwrap_err(),
        VestingError::ZeroTotal
    );
    assert_eq!(
        check_schedule(&continuous(1000, 0, 3), 999).unwrap_err(),
        VestingError::TotalMismatch
    );
    assert_eq!(
        check_schedule(&continuous(1000, -1, 3), 1000).unwrap_err(),
        VestingError::NegativeStart
    );
    let mut schedule = continuous(1000, 0, 3);
    schedule.periods = vec![Period {
        length_seconds: 1,
        amount: 1000,
    }];
    assert_eq!(
        check_schedule(&schedule, 1000).unwrap_err(),
        VestingError::UnexpectedPeriods
    );
}

#[test]
fn check_schedule_rejects_periodic_violations() {
    let mut schedule = periodic();
    schedule.periods = vec![];
    assert_eq!(
        check_schedule(&schedule, 100).unwrap_err(),
        VestingError::PeriodCountMismatch
    );
    let mut schedule = periodic();
    schedule.periods[0].length_seconds = 0;
    assert_eq!(
        check_schedule(&schedule, 100).unwrap_err(),
        VestingError::NonPositiveLength
    );
    let mut schedule = periodic();
    schedule.periods[0].amount = 41;
    assert_eq!(
        check_schedule(&schedule, 100).unwrap_err(),
        VestingError::PeriodAmountSumMismatch
    );
    let mut schedule = periodic();
    schedule.end_at = 131;
    assert_eq!(
        check_schedule(&schedule, 100).unwrap_err(),
        VestingError::PeriodLengthSumMismatch
    );
    let periods: Vec<Period> = (0..513)
        .map(|_| Period {
            length_seconds: 1,
            amount: 1,
        })
        .collect();
    let big = Schedule {
        kind: ScheduleKind::Periodic,
        start_at: 0,
        end_at: 513,
        periods,
        total: 513,
    };
    assert_eq!(
        check_schedule(&big, 513).unwrap_err(),
        VestingError::PeriodCountMismatch
    );
}

#[test]
fn check_schedule_rejects_end_at_over_js_safe_integer() {
    let schedule = continuous(
        1000,
        0,
        distributor_vesting::unlock::MAX_JS_SAFE_INTEGER + 1,
    );
    assert_eq!(
        check_schedule(&schedule, 1000).unwrap_err(),
        VestingError::EndAtTooLarge
    );
}
