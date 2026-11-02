use distributor_test_support::{
    ClaimAccount, ClaimSignature, CosmosKey, InstructionError, PROGRAM_ID, SingleLeafSetup,
    assert_anchor_error, assert_instruction_error, build_single_leaf_setup, claim_ix,
    create_ata_ix, find_vesting_state_address, mint_and_accounts, read_token_amount, send,
    send_expect_err, sign_claim, with_account,
};
use litesvm::LiteSVM;
use solana_sdk::{pubkey::Pubkey, signature::Keypair, signer::Signer};

struct Ctx {
    svm: LiteSVM,
    authority: Keypair,
    payer: Keypair,
    beneficiary: Keypair,
    setup: SingleLeafSetup,
    sig: ClaimSignature,
}

fn ctx() -> Ctx {
    let mut svm = LiteSVM::new();
    let authority = Keypair::new();
    let payer = Keypair::new();
    let beneficiary = Keypair::new();
    for signer in [&authority, &payer] {
        svm.airdrop(&signer.pubkey(), 100_000_000_000).unwrap();
    }
    let cosmos = CosmosKey::from_seed(7);
    let setup = build_single_leaf_setup(&mut svm, &authority, &cosmos, 600, 0);
    let sig = sign_claim(
        &cosmos,
        &beneficiary.pubkey(),
        &setup.roots_commitment,
        &PROGRAM_ID,
    );
    Ctx {
        svm,
        authority,
        payer,
        beneficiary,
        setup,
        sig,
    }
}

fn reject_then_still_claimable(
    ctx: &mut Ctx,
    which: ClaimAccount,
    substitute: Pubkey,
    expected: anchor_lang::error::ErrorCode,
) {
    let honest = claim_ix(
        &ctx.payer,
        &ctx.beneficiary.pubkey(),
        &ctx.setup,
        &ctx.sig,
        600,
        0,
        None,
    );
    let err = send_expect_err(
        &mut ctx.svm,
        &ctx.payer,
        with_account(honest.clone(), which, substitute),
    );
    assert_anchor_error(&err, expected);
    send(&mut ctx.svm, &ctx.payer, honest);
    assert_eq!(
        read_token_amount(
            &ctx.svm,
            &ctx.setup.beneficiary_token(&ctx.beneficiary.pubkey())
        ),
        600
    );
}

#[test]
fn wrong_config_is_rejected() {
    let mut fixture = ctx();
    reject_then_still_claimable(
        &mut fixture,
        ClaimAccount::Config,
        Pubkey::new_unique(),
        anchor_lang::error::ErrorCode::AccountNotInitialized,
    );
}

#[test]
fn wrong_roots_is_rejected() {
    let mut fixture = ctx();

    reject_then_still_claimable(
        &mut fixture,
        ClaimAccount::Roots,
        Pubkey::new_unique(),
        anchor_lang::error::ErrorCode::AccountOwnedByWrongProgram,
    );
}

#[test]
fn foreign_shard_bitmap_is_rejected() {
    let mut fixture = ctx();

    let other_shard = (fixture.setup.shard_id + 1) % distributor_core::consts::SHARD_COUNT;
    let (foreign_bitmap, _) = distributor_core::pda::find_claim_bitmap(&PROGRAM_ID, other_shard);
    reject_then_still_claimable(
        &mut fixture,
        ClaimAccount::Bitmap,
        foreign_bitmap,
        anchor_lang::error::ErrorCode::ConstraintSeeds,
    );
}

#[test]
fn fake_vault_is_rejected() {
    let mut fixture = ctx();

    let decoy = mint_and_accounts(&mut fixture.svm, &fixture.authority, 1, 1, &PROGRAM_ID);
    reject_then_still_claimable(
        &mut fixture,
        ClaimAccount::Vault,
        decoy.vault,
        anchor_lang::error::ErrorCode::ConstraintAddress,
    );
}

#[test]
fn wrong_vault_authority_is_rejected() {
    let mut fixture = ctx();
    reject_then_still_claimable(
        &mut fixture,
        ClaimAccount::VaultAuthority,
        Pubkey::new_unique(),
        anchor_lang::error::ErrorCode::ConstraintSeeds,
    );
}

#[test]
fn wrong_mint_is_rejected() {
    let mut fixture = ctx();

    let decoy = mint_and_accounts(&mut fixture.svm, &fixture.authority, 1, 1, &PROGRAM_ID);
    let honest = claim_ix(
        &fixture.payer,
        &fixture.beneficiary.pubkey(),
        &fixture.setup,
        &fixture.sig,
        600,
        0,
        None,
    );
    let err = send_expect_err(
        &mut fixture.svm,
        &fixture.payer,
        with_account(honest.clone(), ClaimAccount::Mint, decoy.mint),
    );
    assert_instruction_error(&err, InstructionError::MissingAccount);
    send(&mut fixture.svm, &fixture.payer, honest);
    assert_eq!(
        read_token_amount(
            &fixture.svm,
            &fixture
                .setup
                .beneficiary_token(&fixture.beneficiary.pubkey())
        ),
        600
    );
}

#[test]
fn someone_elses_ata_is_rejected() {
    let mut fixture = ctx();

    let payer_ata = fixture.setup.beneficiary_token(&fixture.payer.pubkey());
    let payer_pk = fixture.payer.pubkey();
    let create = create_ata_ix(&payer_pk, &payer_pk, &fixture.setup.mint);
    send(&mut fixture.svm, &fixture.payer, create);
    reject_then_still_claimable(
        &mut fixture,
        ClaimAccount::BeneficiaryToken,
        payer_ata,
        anchor_lang::error::ErrorCode::ConstraintTokenOwner,
    );
}

#[test]
fn foreign_vesting_state_pda_is_rejected() {
    let mut fixture = ctx();
    let foreign = find_vesting_state_address(fixture.setup.shard_id, fixture.setup.leaf_index + 1);
    reject_then_still_claimable(
        &mut fixture,
        ClaimAccount::VestingState,
        foreign,
        anchor_lang::error::ErrorCode::ConstraintSeeds,
    );
}
