use crate::{PROGRAM_ID, credit_vault};
use anchor_lang::{AccountSerialize, Discriminator};
use dvpn::state::{Channel, Grant, Lease, Node, Provider, User};
use dvpn_core::args::NodePrices;
use dvpn_core::constants::MAX_URI_BYTES;
use dvpn_core::enums::{ListingStatus, PositionMode, PositionStatus};
use litesvm::LiteSVM;
use solana_sdk::{account::Account, pubkey::Pubkey};

pub fn plant_account<T: AccountSerialize + Discriminator>(
    svm: &mut LiteSVM,
    address: Pubkey,
    data: &T,
) {
    let mut buf = Vec::new();
    data.try_serialize(&mut buf)
        .expect("account should serialize");
    let lamports = svm.minimum_balance_for_rent_exemption(buf.len());
    svm.set_account(
        address,
        Account {
            lamports,
            data: buf,
            owner: PROGRAM_ID,
            executable: false,
            rent_epoch: 0,
        },
    )
    .expect("set_account: plant account");
}

pub fn plant_system_account(svm: &mut LiteSVM, address: Pubkey, lamports: u64) {
    svm.set_account(
        address,
        Account {
            lamports,
            data: vec![],
            owner: anchor_lang::solana_program::system_program::ID,
            executable: false,
            rent_epoch: 0,
        },
    )
    .expect("set_account: plant system account");
}

fn fixed_uri(uri: &[u8]) -> ([u8; MAX_URI_BYTES], u8) {
    assert!(uri.len() <= MAX_URI_BYTES, "uri exceeds MAX_URI_BYTES");
    let mut fixed = [0u8; MAX_URI_BYTES];
    fixed[..uri.len()].copy_from_slice(uri);
    (
        fixed,
        u8::try_from(uri.len()).expect("MAX_URI_BYTES fits in u8"),
    )
}

#[derive(Default, Clone)]
pub struct NodeSpec {
    pub owner: Pubkey,
    pub node_id: [u8; 32],
    pub status: u8,
    pub listed_at: i64,
    pub prices: NodePrices,
    pub protocols: u32,
    pub country: [u8; 2],
    pub uri: Vec<u8>,
    pub bond: u64,
    pub revenue: u64,
    pub fees_accrued: u64,
    pub rent_recipient: Pubkey,
    pub opened_at: i64,
}

impl NodeSpec {
    pub fn listed(owner: Pubkey, listed_at: i64) -> Self {
        Self {
            owner,
            status: ListingStatus::Listed as u8,
            listed_at,
            bond: 0,
            ..Default::default()
        }
    }
}

pub fn plant_node(svm: &mut LiteSVM, config: Pubkey, spec: &NodeSpec) -> Pubkey {
    let (address, bump) = dvpn_core::seeds::find_node(&PROGRAM_ID, &spec.owner, &spec.node_id);
    let (uri, uri_len) = fixed_uri(&spec.uri);
    let node = Node {
        node_id: spec.node_id,
        owner: spec.owner,
        status: spec.status,
        listed_at: spec.listed_at,
        channel_price_per_gb: spec.prices.channel_price_per_gb,
        channel_price_per_hour: spec.prices.channel_price_per_hour,
        lease_price_per_hour: spec.prices.lease_price_per_hour,
        protocols: spec.protocols,
        country: spec.country,
        uri_len,
        uri,
        bond: spec.bond,
        revenue: spec.revenue,
        fees_accrued: spec.fees_accrued,
        rent_recipient: spec.rent_recipient,
        opened_at: spec.opened_at,
        bump,
        reserved: [0u8; 32],
    };
    plant_account(svm, address, &node);
    let credit = spec
        .revenue
        .checked_add(spec.fees_accrued)
        .and_then(|sum| sum.checked_add(spec.bond))
        .expect("node vault credit overflow");
    credit_vault(svm, config, credit);
    address
}

#[derive(Default, Clone)]
pub struct ProviderSpec {
    pub owner: Pubkey,
    pub provider_id: [u8; 32],
    pub status: u8,
    pub uri: Vec<u8>,
    pub bond: u64,
    pub revenue: u64,
    pub fees_accrued: u64,
    pub balance: u64,
    pub open_plans: u16,
    pub open_leases: u32,
    pub rent_recipient: Pubkey,
    pub opened_at: i64,
}

impl ProviderSpec {
    pub fn listed(owner: Pubkey, provider_id: [u8; 32]) -> Self {
        Self {
            owner,
            provider_id,
            status: ListingStatus::Listed as u8,
            ..Default::default()
        }
    }
}

pub fn plant_provider(svm: &mut LiteSVM, config: Pubkey, spec: &ProviderSpec) -> Pubkey {
    let (address, bump) =
        dvpn_core::seeds::find_provider(&PROGRAM_ID, &spec.owner, &spec.provider_id);
    let (uri, uri_len) = fixed_uri(&spec.uri);
    let provider = Provider {
        provider_id: spec.provider_id,
        owner: spec.owner,
        status: spec.status,
        uri_len,
        uri,
        bond: spec.bond,
        revenue: spec.revenue,
        fees_accrued: spec.fees_accrued,
        balance: spec.balance,
        open_plans: spec.open_plans,
        open_leases: spec.open_leases,
        rent_recipient: spec.rent_recipient,
        opened_at: spec.opened_at,
        bump,
        reserved: [0u8; 32],
    };
    plant_account(svm, address, &provider);
    let credit = spec
        .revenue
        .checked_add(spec.fees_accrued)
        .and_then(|sum| sum.checked_add(spec.balance))
        .and_then(|sum| sum.checked_add(spec.bond))
        .expect("provider vault credit overflow");
    credit_vault(svm, config, credit);
    address
}

#[derive(Default, Clone)]
pub struct UserSpec {
    pub owner: Pubkey,
    pub balance: u64,
    pub open_channels: u32,
}

pub fn plant_user(svm: &mut LiteSVM, config: Pubkey, spec: &UserSpec) -> Pubkey {
    let (address, bump) = dvpn_core::seeds::find_user(&PROGRAM_ID, &spec.owner);
    let user = User {
        owner: spec.owner,
        balance: spec.balance,
        open_channels: spec.open_channels,
        rent_recipient: spec.owner,
        bump,
        reserved: [0u8; 32],
    };
    plant_account(svm, address, &user);
    credit_vault(svm, config, spec.balance);
    address
}

#[derive(Default, Clone)]
pub struct ChannelSpec {
    pub owner: Pubkey,
    pub channel_id: [u8; 32],
    pub node: Pubkey,
    pub opener: Pubkey,
    pub mode: u8,
    pub voucher_signer: Pubkey,
    pub rate_per_hour: u64,
    pub stream_base: u64,
    pub stream_started_at: i64,
    pub escrow: u64,
    pub redeemed: u64,
    pub opened_at: i64,
    pub close_started_at: i64,
    pub status: u8,
    pub rent_recipient: Pubkey,
    pub node_opened_at: i64,
}

impl ChannelSpec {
    pub fn voucher(owner: Pubkey, channel_id: [u8; 32]) -> Self {
        Self {
            owner,
            channel_id,
            opener: owner,
            voucher_signer: owner,
            mode: PositionMode::Voucher as u8,
            status: PositionStatus::Open as u8,
            ..Default::default()
        }
    }

    pub fn voucher_on_node(owner: Pubkey, channel_id: [u8; 32], node: Pubkey, escrow: u64) -> Self {
        Self {
            node,
            escrow,
            ..Self::voucher(owner, channel_id)
        }
    }
}

pub fn plant_channel(svm: &mut LiteSVM, config: Pubkey, spec: &ChannelSpec) -> Pubkey {
    let (address, bump) =
        dvpn_core::seeds::find_channel(&PROGRAM_ID, &spec.owner, &spec.channel_id);
    let channel = Channel {
        channel_id: spec.channel_id,
        owner: spec.owner,
        node: spec.node,
        opener: spec.opener,
        mode: spec.mode,
        voucher_signer: spec.voucher_signer,
        rate_per_hour: spec.rate_per_hour,
        stream_base: spec.stream_base,
        stream_started_at: spec.stream_started_at,
        escrow: spec.escrow,
        redeemed: spec.redeemed,
        opened_at: spec.opened_at,
        close_started_at: spec.close_started_at,
        status: spec.status,
        rent_recipient: spec.rent_recipient,
        node_opened_at: spec.node_opened_at,
        bump,
        reserved: [0u8; 32],
    };
    plant_account(svm, address, &channel);
    let credit = spec
        .escrow
        .checked_sub(spec.redeemed)
        .expect("channel redeemed must not exceed escrow");
    credit_vault(svm, config, credit);
    address
}

#[derive(Default, Clone)]
pub struct GrantSpec {
    pub granter: Pubkey,
    pub grantee: Pubkey,
    pub target: Pubkey,
    pub permissions: u64,
    pub expires_at: i64,
    pub allowance: u64,
}

pub fn plant_grant(svm: &mut LiteSVM, spec: &GrantSpec) -> Pubkey {
    let (address, bump) =
        dvpn_core::seeds::find_grant(&PROGRAM_ID, &spec.granter, &spec.grantee, &spec.target);
    let grant = Grant {
        granter: spec.granter,
        grantee: spec.grantee,
        target: spec.target,
        permissions: spec.permissions,
        expires_at: spec.expires_at,
        allowance: spec.allowance,
        rent_recipient: spec.granter,
        created_at: 0,
        bump,
        reserved: [0u8; 32],
    };
    plant_account(svm, address, &grant);
    address
}

#[derive(Default, Clone)]
pub struct LeaseSpec {
    pub provider: Pubkey,
    pub lease_id: [u8; 32],
    pub node: Pubkey,
    pub owner: Pubkey,
    pub opener: Pubkey,
    pub mode: u8,
    pub voucher_signer: Pubkey,
    pub rate_per_hour: u64,
    pub stream_base: u64,
    pub stream_started_at: i64,
    pub escrow: u64,
    pub redeemed: u64,
    pub opened_at: i64,
    pub close_started_at: i64,
    pub status: u8,
    pub rent_recipient: Pubkey,
    pub node_opened_at: i64,
}

impl LeaseSpec {
    pub fn voucher(provider: Pubkey, owner: Pubkey, lease_id: [u8; 32]) -> Self {
        Self {
            provider,
            owner,
            lease_id,
            opener: owner,
            voucher_signer: owner,
            mode: PositionMode::Voucher as u8,
            status: PositionStatus::Open as u8,
            ..Default::default()
        }
    }

    pub fn voucher_on_node(
        provider: Pubkey,
        owner: Pubkey,
        lease_id: [u8; 32],
        node: Pubkey,
        escrow: u64,
    ) -> Self {
        Self {
            node,
            escrow,
            ..Self::voucher(provider, owner, lease_id)
        }
    }
}

pub fn plant_lease(svm: &mut LiteSVM, config: Pubkey, spec: &LeaseSpec) -> Pubkey {
    let (address, bump) = dvpn_core::seeds::find_lease(&PROGRAM_ID, &spec.provider, &spec.lease_id);
    let lease = Lease {
        lease_id: spec.lease_id,
        provider: spec.provider,
        node: spec.node,
        owner: spec.owner,
        opener: spec.opener,
        mode: spec.mode,
        voucher_signer: spec.voucher_signer,
        rate_per_hour: spec.rate_per_hour,
        stream_base: spec.stream_base,
        stream_started_at: spec.stream_started_at,
        escrow: spec.escrow,
        redeemed: spec.redeemed,
        opened_at: spec.opened_at,
        close_started_at: spec.close_started_at,
        status: spec.status,
        rent_recipient: spec.rent_recipient,
        node_opened_at: spec.node_opened_at,
        bump,
        reserved: [0u8; 32],
    };
    plant_account(svm, address, &lease);
    let credit = spec
        .escrow
        .checked_sub(spec.redeemed)
        .expect("lease redeemed must not exceed escrow");
    credit_vault(svm, config, credit);
    address
}
