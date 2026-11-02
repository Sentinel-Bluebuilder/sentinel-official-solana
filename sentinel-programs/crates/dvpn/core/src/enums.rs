use anchor_lang::prelude::*;

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub enum PositionMode {
    Voucher = 0,
    Stream = 1,
}

#[derive(AnchorSerialize, AnchorDeserialize, Clone, PartialEq, Eq, Debug)]
pub enum PositionTerms {
    Voucher { voucher_signer: Pubkey },
    Stream { expected_rate_per_hour: u64 },
}

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub enum ListingStatus {
    Delisted = 0,
    Listed = 1,
}

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub enum PositionStatus {
    Open = 0,
    Closing = 1,
}

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub enum SubscriptionCloseReason {
    Owner = 1,
    Expired = 2,
    Provider = 3,
    Grantee = 4,
}
