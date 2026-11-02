use anchor_lang::{InstructionData, ToAccountMetas};
use dvpn_core::enums::PositionTerms;
use dvpn_core::seeds::{find_channel, find_grant, find_lease, find_user};
use dvpn_core::voucher::Voucher;
use solana_sdk::{
    instruction::{AccountMeta, Instruction},
    pubkey::Pubkey,
};

use crate::Genesis;

pub fn find_grant_if_delegated(owner: &Pubkey, signer: &Pubkey) -> Option<Pubkey> {
    (signer != owner).then(|| find_grant(&dvpn::ID, owner, signer, &Pubkey::default()).0)
}

#[allow(clippy::too_many_arguments)]
pub fn open_channel_ix(
    genesis: &Genesis,
    owner: &Pubkey,
    node: &Pubkey,
    owner_token: &Pubkey,
    channel_id: [u8; 32],
    escrow: u64,
    terms: PositionTerms,
    signer_and_rent_payer: &Pubkey,
) -> Instruction {
    open_channel_ix_by(
        signer_and_rent_payer,
        None,
        genesis,
        owner,
        node,
        owner_token,
        channel_id,
        escrow,
        terms,
        signer_and_rent_payer,
        signer_and_rent_payer,
        signer_and_rent_payer,
    )
}

#[allow(clippy::too_many_arguments)]
pub fn open_channel_ix_by(
    signer: &Pubkey,
    grant: Option<Pubkey>,
    genesis: &Genesis,
    owner: &Pubkey,
    node: &Pubkey,
    owner_token: &Pubkey,
    channel_id: [u8; 32],
    escrow: u64,
    terms: PositionTerms,
    rent_payer: &Pubkey,
    channel_rent_recipient: &Pubkey,
    user_rent_recipient: &Pubkey,
) -> Instruction {
    let (channel, _) = find_channel(&dvpn::ID, owner, &channel_id);
    let (user, _) = find_user(&dvpn::ID, owner);
    Instruction {
        program_id: dvpn::ID,
        accounts: dvpn::accounts::OpenChannel {
            signer: *signer,
            config: genesis.config,
            owner: *owner,
            grant,
            user,
            node: *node,
            channel,
            vault: genesis.vault,
            mint: genesis.mint,
            owner_token: *owner_token,
            token_program: spl_token_2022_interface::id(),
            rent_payer: *rent_payer,
            system_program: anchor_lang::solana_program::system_program::ID,
        }
        .to_account_metas(None),
        data: dvpn::instruction::OpenChannel {
            channel_id,
            escrow,
            terms,
            channel_rent_recipient: *channel_rent_recipient,
            user_rent_recipient: *user_rent_recipient,
        }
        .data(),
    }
}

pub fn fund_channels_ix(
    genesis: &Genesis,
    owner: &Pubkey,
    owner_token: &Pubkey,
    channels: &[Pubkey],
    amounts: Vec<u64>,
) -> Instruction {
    fund_channels_ix_by(owner, None, genesis, owner, owner_token, channels, amounts)
}

#[allow(clippy::too_many_arguments)]
pub fn fund_channels_ix_as_grantee(
    genesis: &Genesis,
    owner: &Pubkey,
    grantee: &Pubkey,
    grant: &Pubkey,
    owner_token: &Pubkey,
    channels: &[Pubkey],
    amounts: Vec<u64>,
) -> Instruction {
    fund_channels_ix_by(
        grantee,
        Some(*grant),
        genesis,
        owner,
        owner_token,
        channels,
        amounts,
    )
}

#[allow(clippy::too_many_arguments)]
fn fund_channels_ix_by(
    signer: &Pubkey,
    grant: Option<Pubkey>,
    genesis: &Genesis,
    owner: &Pubkey,
    owner_token: &Pubkey,
    channels: &[Pubkey],
    amounts: Vec<u64>,
) -> Instruction {
    let (user, _) = find_user(&dvpn::ID, owner);
    let mut accounts = dvpn::accounts::FundChannels {
        signer: *signer,
        config: genesis.config,
        owner: *owner,
        user,
        grant,
        vault: genesis.vault,
        mint: genesis.mint,
        owner_token: *owner_token,
        token_program: spl_token_2022_interface::id(),
    }
    .to_account_metas(None);
    accounts.extend(
        channels
            .iter()
            .map(|channel_address| AccountMeta::new(*channel_address, false)),
    );
    Instruction {
        program_id: dvpn::ID,
        accounts,
        data: dvpn::instruction::FundChannels { amounts }.data(),
    }
}

pub fn redeem_channel_vouchers_ix(
    genesis: &Genesis,
    redeemer: &Pubkey,
    node: &Pubkey,
    channels: &[Pubkey],
    vouchers: Vec<Voucher>,
) -> Instruction {
    redeem_channel_vouchers_ix_with_accounts(genesis.config, redeemer, node, channels, vouchers)
}

pub fn redeem_channel_vouchers_ix_with_accounts(
    config: Pubkey,
    redeemer: &Pubkey,
    node: &Pubkey,
    channels: &[Pubkey],
    vouchers: Vec<Voucher>,
) -> Instruction {
    let mut accounts = dvpn::accounts::RedeemChannelVouchers {
        signer: *redeemer,
        config,
        node: *node,
    }
    .to_account_metas(None);
    accounts.extend(
        channels
            .iter()
            .map(|channel_address| AccountMeta::new(*channel_address, false)),
    );
    Instruction {
        program_id: dvpn::ID,
        accounts,
        data: dvpn::instruction::RedeemChannelVouchers { vouchers }.data(),
    }
}

pub fn redeem_channel_streams_ix(
    genesis: &Genesis,
    redeemer: &Pubkey,
    node: &Pubkey,
    channels: &[Pubkey],
) -> Instruction {
    redeem_channel_streams_ix_with_accounts(genesis.config, redeemer, node, channels)
}

pub fn redeem_channel_streams_ix_with_accounts(
    config: Pubkey,
    redeemer: &Pubkey,
    node: &Pubkey,
    channels: &[Pubkey],
) -> Instruction {
    let mut accounts = dvpn::accounts::RedeemChannelStreams {
        signer: *redeemer,
        config,
        node: *node,
    }
    .to_account_metas(None);
    accounts.extend(
        channels
            .iter()
            .map(|channel_address| AccountMeta::new(*channel_address, false)),
    );
    Instruction {
        program_id: dvpn::ID,
        accounts,
        data: dvpn::instruction::RedeemChannelStreams {}.data(),
    }
}

pub fn start_close_channel_ix(genesis: &Genesis, channel: &Pubkey, signer: &Pubkey) -> Instruction {
    start_close_channel_ix_by(signer, None, genesis, channel)
}

pub fn start_close_channel_ix_by(
    signer: &Pubkey,
    grant: Option<Pubkey>,
    genesis: &Genesis,
    channel: &Pubkey,
) -> Instruction {
    Instruction {
        program_id: dvpn::ID,
        accounts: dvpn::accounts::StartCloseChannel {
            signer: *signer,
            config: genesis.config,
            channel: *channel,
            grant,
        }
        .to_account_metas(None),
        data: dvpn::instruction::StartCloseChannel {}.data(),
    }
}

pub fn close_channel_ix(
    genesis: &Genesis,
    channel: &Pubkey,
    signer: &Pubkey,
    node: Option<&Pubkey>,
    rent_recipient: &Pubkey,
) -> Instruction {
    close_channel_ix_by(signer, None, genesis, channel, node, rent_recipient)
}

pub fn close_channel_ix_by(
    signer: &Pubkey,
    grant: Option<Pubkey>,
    genesis: &Genesis,
    channel: &Pubkey,
    node: Option<&Pubkey>,
    rent_recipient: &Pubkey,
) -> Instruction {
    let owner = crate::read_channel(&genesis.svm, channel).owner;
    close_channel_ix_with_accounts(
        genesis.config,
        &owner,
        channel,
        signer,
        grant,
        node,
        rent_recipient,
    )
}

#[allow(clippy::too_many_arguments)]
pub fn close_channel_ix_with_accounts(
    config: Pubkey,
    owner: &Pubkey,
    channel: &Pubkey,
    signer: &Pubkey,
    grant: Option<Pubkey>,
    node: Option<&Pubkey>,
    rent_recipient: &Pubkey,
) -> Instruction {
    let (user, _) = find_user(&dvpn::ID, owner);
    Instruction {
        program_id: dvpn::ID,
        accounts: dvpn::accounts::CloseChannel {
            signer: *signer,
            config,
            channel: *channel,
            user,
            grant,
            node: node.copied(),
            rent_recipient: *rent_recipient,
        }
        .to_account_metas(None),
        data: dvpn::instruction::CloseChannel {}.data(),
    }
}

#[allow(clippy::too_many_arguments)]
pub fn open_lease_ix(
    genesis: &Genesis,
    provider: &Pubkey,
    node: &Pubkey,
    owner_token: &Pubkey,
    lease_id: [u8; 32],
    escrow: u64,
    terms: PositionTerms,
    signer_and_rent_payer: &Pubkey,
    lease_rent_recipient: &Pubkey,
) -> Instruction {
    let (lease, _) = find_lease(&dvpn::ID, provider, &lease_id);
    Instruction {
        program_id: dvpn::ID,
        accounts: dvpn::accounts::OpenLease {
            signer: *signer_and_rent_payer,
            config: genesis.config,
            provider: *provider,
            grant: None,
            node: *node,
            lease,
            vault: genesis.vault,
            mint: genesis.mint,
            owner_token: *owner_token,
            token_program: spl_token_2022_interface::id(),
            rent_payer: *signer_and_rent_payer,
            system_program: anchor_lang::solana_program::system_program::ID,
        }
        .to_account_metas(None),
        data: dvpn::instruction::OpenLease {
            lease_id,
            escrow,
            terms,
            lease_rent_recipient: *lease_rent_recipient,
        }
        .data(),
    }
}

#[allow(clippy::too_many_arguments)]
pub fn open_lease_ix_as_grantee(
    genesis: &Genesis,
    provider: &Pubkey,
    grantee: &Pubkey,
    grant: &Pubkey,
    node: &Pubkey,
    owner_token: &Pubkey,
    lease_id: [u8; 32],
    escrow: u64,
    terms: PositionTerms,
    rent_payer: &Pubkey,
) -> Instruction {
    open_lease_ix_as_grantee_with_accounts(
        genesis.config,
        genesis.vault,
        genesis.mint,
        provider,
        grantee,
        grant,
        node,
        owner_token,
        lease_id,
        escrow,
        terms,
        rent_payer,
        rent_payer,
    )
}

#[allow(clippy::too_many_arguments)]
pub fn open_lease_ix_as_grantee_with_accounts(
    config: Pubkey,
    vault: Pubkey,
    mint: Pubkey,
    provider: &Pubkey,
    grantee: &Pubkey,
    grant: &Pubkey,
    node: &Pubkey,
    owner_token: &Pubkey,
    lease_id: [u8; 32],
    escrow: u64,
    terms: PositionTerms,
    rent_payer: &Pubkey,
    lease_rent_recipient: &Pubkey,
) -> Instruction {
    let (lease, _) = find_lease(&dvpn::ID, provider, &lease_id);
    Instruction {
        program_id: dvpn::ID,
        accounts: dvpn::accounts::OpenLease {
            signer: *grantee,
            config,
            provider: *provider,
            grant: Some(*grant),
            node: *node,
            lease,
            vault,
            mint,
            owner_token: *owner_token,
            token_program: spl_token_2022_interface::id(),
            rent_payer: *rent_payer,
            system_program: anchor_lang::solana_program::system_program::ID,
        }
        .to_account_metas(None),
        data: dvpn::instruction::OpenLease {
            lease_id,
            escrow,
            terms,
            lease_rent_recipient: *lease_rent_recipient,
        }
        .data(),
    }
}

pub fn fund_leases_ix(
    genesis: &Genesis,
    provider: &Pubkey,
    owner_token: &Pubkey,
    leases: &[Pubkey],
    amounts: Vec<u64>,
) -> Instruction {
    let provider_owner = crate::read_provider(&genesis.svm, provider).owner;
    fund_leases_ix_by(
        &provider_owner,
        None,
        genesis,
        provider,
        owner_token,
        leases,
        amounts,
    )
}

pub fn fund_leases_ix_as_grantee(
    genesis: &Genesis,
    provider: &Pubkey,
    grantee: &Pubkey,
    grant: &Pubkey,
    owner_token: &Pubkey,
    leases: &[Pubkey],
    amounts: Vec<u64>,
) -> Instruction {
    fund_leases_ix_by(
        grantee,
        Some(*grant),
        genesis,
        provider,
        owner_token,
        leases,
        amounts,
    )
}

fn fund_leases_ix_by(
    signer: &Pubkey,
    grant: Option<Pubkey>,
    genesis: &Genesis,
    provider: &Pubkey,
    owner_token: &Pubkey,
    leases: &[Pubkey],
    amounts: Vec<u64>,
) -> Instruction {
    let mut accounts = dvpn::accounts::FundLeases {
        signer: *signer,
        config: genesis.config,
        provider: *provider,
        grant,
        vault: genesis.vault,
        mint: genesis.mint,
        owner_token: *owner_token,
        token_program: spl_token_2022_interface::id(),
    }
    .to_account_metas(None);
    accounts.extend(
        leases
            .iter()
            .map(|lease_address| AccountMeta::new(*lease_address, false)),
    );
    Instruction {
        program_id: dvpn::ID,
        accounts,
        data: dvpn::instruction::FundLeases { amounts }.data(),
    }
}

pub fn redeem_lease_streams_ix(
    genesis: &Genesis,
    redeemer: &Pubkey,
    node: &Pubkey,
    leases: &[Pubkey],
) -> Instruction {
    let mut accounts = dvpn::accounts::RedeemLeaseStreams {
        signer: *redeemer,
        config: genesis.config,
        node: *node,
    }
    .to_account_metas(None);
    accounts.extend(
        leases
            .iter()
            .map(|lease_address| AccountMeta::new(*lease_address, false)),
    );
    Instruction {
        program_id: dvpn::ID,
        accounts,
        data: dvpn::instruction::RedeemLeaseStreams {}.data(),
    }
}

pub fn redeem_lease_vouchers_ix(
    genesis: &Genesis,
    redeemer: &Pubkey,
    node: &Pubkey,
    leases: &[Pubkey],
    vouchers: Vec<Voucher>,
) -> Instruction {
    redeem_lease_vouchers_ix_with_accounts(genesis.config, redeemer, node, leases, vouchers)
}

pub fn redeem_lease_vouchers_ix_with_accounts(
    config: Pubkey,
    redeemer: &Pubkey,
    node: &Pubkey,
    leases: &[Pubkey],
    vouchers: Vec<Voucher>,
) -> Instruction {
    let mut accounts = dvpn::accounts::RedeemLeaseVouchers {
        signer: *redeemer,
        config,
        node: *node,
    }
    .to_account_metas(None);
    accounts.extend(
        leases
            .iter()
            .map(|lease_address| AccountMeta::new(*lease_address, false)),
    );
    Instruction {
        program_id: dvpn::ID,
        accounts,
        data: dvpn::instruction::RedeemLeaseVouchers { vouchers }.data(),
    }
}

pub fn start_close_lease_ix(
    genesis: &Genesis,
    provider: &Pubkey,
    lease: &Pubkey,
    signer: &Pubkey,
) -> Instruction {
    start_close_lease_ix_by(signer, None, genesis, provider, lease)
}

pub fn start_close_lease_ix_by(
    signer: &Pubkey,
    grant: Option<Pubkey>,
    genesis: &Genesis,
    provider: &Pubkey,
    lease: &Pubkey,
) -> Instruction {
    Instruction {
        program_id: dvpn::ID,
        accounts: dvpn::accounts::StartCloseLease {
            signer: *signer,
            config: genesis.config,
            provider: *provider,
            lease: *lease,
            grant,
        }
        .to_account_metas(None),
        data: dvpn::instruction::StartCloseLease {}.data(),
    }
}

pub fn close_lease_ix(
    genesis: &Genesis,
    provider: &Pubkey,
    lease: &Pubkey,
    signer: &Pubkey,
    node: Option<Pubkey>,
    rent_recipient: &Pubkey,
) -> Instruction {
    close_lease_ix_by(signer, None, genesis, provider, lease, node, rent_recipient)
}

pub fn close_lease_ix_by(
    signer: &Pubkey,
    grant: Option<Pubkey>,
    genesis: &Genesis,
    provider: &Pubkey,
    lease: &Pubkey,
    node: Option<Pubkey>,
    rent_recipient: &Pubkey,
) -> Instruction {
    Instruction {
        program_id: dvpn::ID,
        accounts: dvpn::accounts::CloseLease {
            signer: *signer,
            config: genesis.config,
            provider: *provider,
            lease: *lease,
            grant,
            node,
            rent_recipient: *rent_recipient,
        }
        .to_account_metas(None),
        data: dvpn::instruction::CloseLease {}.data(),
    }
}

#[allow(clippy::too_many_arguments)]
pub fn open_channel_ix_as_grantee_with_accounts(
    config: Pubkey,
    vault: Pubkey,
    mint: Pubkey,
    owner: &Pubkey,
    grantee: &Pubkey,
    grant: &Pubkey,
    node: &Pubkey,
    owner_token: &Pubkey,
    channel_id: [u8; 32],
    escrow: u64,
    terms: PositionTerms,
    rent_payer: &Pubkey,
    channel_rent_recipient: &Pubkey,
    user_rent_recipient: &Pubkey,
) -> Instruction {
    let (channel, _) = find_channel(&dvpn::ID, owner, &channel_id);
    let (user, _) = find_user(&dvpn::ID, owner);
    Instruction {
        program_id: dvpn::ID,
        accounts: dvpn::accounts::OpenChannel {
            signer: *grantee,
            config,
            owner: *owner,
            grant: Some(*grant),
            user,
            node: *node,
            channel,
            vault,
            mint,
            owner_token: *owner_token,
            token_program: spl_token_2022_interface::id(),
            rent_payer: *rent_payer,
            system_program: anchor_lang::solana_program::system_program::ID,
        }
        .to_account_metas(None),
        data: dvpn::instruction::OpenChannel {
            channel_id,
            escrow,
            terms,
            channel_rent_recipient: *channel_rent_recipient,
            user_rent_recipient: *user_rent_recipient,
        }
        .data(),
    }
}
