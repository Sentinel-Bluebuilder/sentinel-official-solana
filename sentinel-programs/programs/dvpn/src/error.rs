use anchor_lang::prelude::*;

#[error_code]
pub enum DvpnError {
    #[msg("signer is not authorized for this action")]
    Unauthorized,
    #[msg("this operation is paused")]
    OperationPaused,
    #[msg("fee bps out of range")]
    FeeBpsOutOfRange,
    #[msg("parameter out of range")]
    ParamOutOfRange,
    #[msg("pending authority is missing")]
    MissingPendingAuthority,
    #[msg("mint is invalid")]
    InvalidMint,
    #[msg("mint has a forbidden extension")]
    MintExtensionForbidden,
    #[msg("mint is not sealed")]
    MintNotSealed,
    #[msg("vault is not the config associated token account")]
    VaultNotAssociated,
    #[msg("vault is not immutable-owner")]
    VaultNotImmutable,
    #[msg("treasury may not be the vault or config-owned")]
    InvalidTreasury,
    #[msg("bond below minimum")]
    BondBelowMinimum,
    #[msg("a parked bond is pending for this id")]
    BondPending,
    #[msg("unbonding window not elapsed")]
    UnbondingNotElapsed,
    #[msg("uri too long")]
    UriTooLong,
    #[msg("node is not listed")]
    NodeNotListed,
    #[msg("node is not live")]
    NodeNotLive,
    #[msg("node price changed")]
    NodePriceChanged,
    #[msg("insufficient revenue")]
    InsufficientRevenue,
    #[msg("fees to sweep are zero")]
    FeesZero,
    #[msg("provider is not listed")]
    ProviderNotListed,
    #[msg("open plans remain")]
    OpenPlansRemain,
    #[msg("open leases remain")]
    OpenLeasesRemain,
    #[msg("balance to withdraw is zero")]
    BalanceZero,
    #[msg("amount exceeds allowance")]
    AmountExceedsAllowance,
    #[msg("plan is not listed")]
    PlanNotListed,
    #[msg("plan is not delisted")]
    PlanNotDelisted,
    #[msg("plan terms changed")]
    PlanTermsChanged,
    #[msg("subscription not expired")]
    SubscriptionNotExpired,
    #[msg("subscription price is not zero")]
    SubscriptionPriceNotZero,
    #[msg("lease is not open")]
    LeaseNotOpen,
    #[msg("lease is not closing")]
    LeaseNotClosing,
    #[msg("channel is not open")]
    ChannelNotOpen,
    #[msg("channel is not closing")]
    ChannelNotClosing,
    #[msg("voucher exceeds escrow")]
    VoucherExceedsEscrow,
    #[msg("grace window not elapsed")]
    GraceNotElapsed,
    #[msg("redeemable amount is zero")]
    RedeemableZero,
    #[msg("position mode is not voucher")]
    ModeNotVoucher,
    #[msg("position mode is not stream")]
    ModeNotStream,
    #[msg("voucher signature is invalid")]
    InvalidVoucherSignature,
    #[msg("batch length mismatch")]
    BatchLengthMismatch,
    #[msg("batch not sorted")]
    BatchNotSorted,
    #[msg("position belongs to an earlier incarnation of this node")]
    NodeReopened,
    #[msg("arithmetic overflow")]
    ArithmeticOverflow,
    #[msg("vault is frozen")]
    VaultFrozen,
    #[msg("grant has expired")]
    GrantExpired,
    #[msg("grant does not include the permission for this action")]
    PermissionNotGranted,
    #[msg("grant target does not cover this account")]
    GrantTargetMismatch,
    #[msg("voucher signer is not a valid key")]
    InvalidVoucherSigner,
    #[msg("rent recipient is missing")]
    MissingRentRecipient,
    #[msg("rent recipient is neither the signer nor the rent payer")]
    InvalidRentRecipient,
    #[msg("voucher claim exceeds the signed amount")]
    VoucherClaimExceedsSigned,
}
