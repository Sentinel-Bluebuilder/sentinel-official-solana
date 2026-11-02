use ceremony::exit_code;

#[test]
fn shared_exit_codes_are_pinned() {
    assert_eq!(exit_code::OPERATIONAL, 1);
    assert_eq!(exit_code::GATE_REFUSED, 3);
    assert_eq!(exit_code::TRANSIENT, 4);
    assert_eq!(exit_code::USAGE, 5);
}
