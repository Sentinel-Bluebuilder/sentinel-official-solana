use anchor_lang::{InstructionData, ToAccountMetas};
use dvpn::ID;
use dvpn_core::seeds::{find_config, find_user, vault_ata_address};
use solana_sdk::{instruction::Instruction, pubkey::Pubkey};

pub fn withdraw_user_balance_ix(owner: &Pubkey, owner_token: Pubkey, mint: Pubkey) -> Instruction {
    withdraw_user_balance_ix_by(owner, None, owner, *owner, owner_token, mint)
}

pub fn withdraw_user_balance_ix_by(
    signer: &Pubkey,
    grant: Option<Pubkey>,
    owner: &Pubkey,
    rent_recipient: Pubkey,
    owner_token: Pubkey,
    mint: Pubkey,
) -> Instruction {
    let user = find_user(&ID, owner).0;
    let (config, _bump) = find_config(&ID);
    let vault = vault_ata_address(&config, &mint);
    Instruction {
        program_id: ID,
        accounts: dvpn::accounts::WithdrawUserBalance {
            signer: *signer,
            config,
            owner: *owner,
            grant,
            user,
            rent_recipient,
            owner_token,
            vault,
            mint,
            token_program: spl_token_2022_interface::id(),
        }
        .to_account_metas(None),
        data: dvpn::instruction::WithdrawUserBalance {}.data(),
    }
}
