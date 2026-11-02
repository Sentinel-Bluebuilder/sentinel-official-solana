use distributor::instructions::ALLOWED_MINT_EXTENSIONS as DISTRIBUTOR_ALLOWED;
use dvpn_core::constants::ALLOWED_MINT_EXTENSIONS as DVPN_ALLOWED;

fn distributor_allowed_extensions() -> Vec<[u8; 2]> {
    DISTRIBUTOR_ALLOWED
        .iter()
        .map(|entry| (*entry).into())
        .collect()
}

fn dvpn_allowed_extensions() -> Vec<[u8; 2]> {
    DVPN_ALLOWED.iter().map(|entry| (*entry).into()).collect()
}

#[test]
fn allowed_mint_extensions_match_distributor() {
    let dvpn_allowed = dvpn_allowed_extensions();
    let distributor_allowed = distributor_allowed_extensions();
    assert_eq!(dvpn_allowed.len(), distributor_allowed.len());
    for entry in &distributor_allowed {
        assert!(dvpn_allowed.contains(entry), "dvpn is missing {entry:?}");
    }
    for entry in &dvpn_allowed {
        assert!(
            distributor_allowed.contains(entry),
            "dvpn has extra {entry:?}"
        );
    }
}
