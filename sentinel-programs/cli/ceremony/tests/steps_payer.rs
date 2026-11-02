use ceremony::sender::ROOTS_PER_CHUNK;
use ceremony::steps::{CeremonyAddresses, CeremonyInputs, RentTable, build_ceremony_steps};
use distributor_mint::create::TokenMetadataParams;
use solana_sdk::{signature::Keypair, signer::Signer};

fn ceremony_addresses() -> CeremonyAddresses {
    CeremonyAddresses {
        upgrade_authority: Keypair::new().pubkey(),
        payer: Keypair::new().pubkey(),
        mint: Keypair::new().pubkey(),
        treasury: Keypair::new().pubkey(),
        vault: Keypair::new().pubkey(),
    }
}

#[test]
fn create_mint_is_funded_by_payer_not_upgrade_authority() {
    let addresses = ceremony_addresses();
    let inputs = CeremonyInputs {
        program_id: Keypair::new().pubkey(),
        token_metadata: TokenMetadataParams {
            name: "S".into(),
            symbol: "S".into(),
            uri: "https://sentinel.co/metadata.json".into(),
        },
        roots: vec![[0u8; 32]; ROOTS_PER_CHUNK + 1],
        shard_leaf_counts: vec![0u32; distributor_core::consts::SHARD_COUNT as usize],
        vault_amount: 1,
        treasury_amount: 1,
        expected_roots_commitment: [0u8; 32],
    };
    let rent = RentTable {
        mint_at_create_lamports: 1,
        mint_with_metadata_lamports: 2,
        token_account_lamports: 1,
    };
    let steps = build_ceremony_steps(&inputs, &addresses, &rent, ROOTS_PER_CHUNK);

    let create_mint = steps
        .iter()
        .find(|step| step.name == "distributor_create_mint")
        .unwrap();
    assert_eq!(create_mint.ixs[0].accounts[0].pubkey, addresses.payer);
    assert_ne!(
        create_mint.ixs[0].accounts[0].pubkey,
        addresses.upgrade_authority
    );

    let chunks = steps
        .iter()
        .filter(|step| step.name.starts_with("distributor_write_roots_chunk_"))
        .count();
    assert_eq!(chunks, 2);
}
