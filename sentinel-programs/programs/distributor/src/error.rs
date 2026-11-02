use anchor_lang::prelude::*;

#[error_code]
pub enum DistributorError {
    #[msg("signer is not the program upgrade authority")]
    Unauthorized,
    #[msg("genesis already finalized")]
    ConfigAlreadyFinalized,
    #[msg("genesis not finalized")]
    ConfigNotFinalized,
    #[msg("recomputed roots commitment does not match the expected value")]
    RootsCommitmentMismatch,
    #[msg("vault balance does not equal claimable total")]
    VaultBalanceMismatch,
    #[msg("treasury + vault does not equal mint supply")]
    SupplyMismatch,
    #[msg("mint authority or freeze authority is still set")]
    MintNotSealed,
    #[msg("roots chunk out of range")]
    ChunkOutOfRange,
    #[msg("not all SHARD_COUNT shard bitmaps were created")]
    BitmapCountMismatch,
    #[msg("mint has unexpected decimals")]
    MintDecimalsMismatch,
    #[msg("leaf index past the bitmap's capacity")]
    LeafIndexOutOfRange,
    #[msg("shard_id out of range")]
    ShardIdOutOfRange,
    #[msg("leaf_count exceeds the builder's per-shard cap")]
    ShardLeafCountTooLarge,
    #[msg("shard identifier does not match the expected shard")]
    ShardIdMismatch,
    #[msg("bech32 address is malformed or non-canonical")]
    InvalidBech32Address,
    #[msg("signature is malformed, high-S, or does not recover")]
    InvalidSignature,
    #[msg("merkle proof does not verify against the shard root")]
    InvalidProof,
    #[msg("leaf has already been claimed")]
    LeafAlreadyClaimed,
    #[msg("a schedule was supplied for a liquid-only (locked_amount == 0) leaf")]
    UnexpectedSchedule,
    #[msg("a locked leaf requires a schedule")]
    MissingSchedule,
    #[msg("recomputed schedule hash does not match the leaf's vesting_schedule_hash")]
    ScheduleHashMismatch,
    #[msg("schedule shape is invalid")]
    InvalidSchedule,
    #[msg("integer overflow")]
    ArithmeticOverflow,
    #[msg("nothing newly unlocked to withdraw")]
    WithdrawableZero,
    #[msg("a claim's schedule buffer has not been finalized")]
    BufferNotFinalized,
    #[msg("a schedule buffer is already finalized")]
    BufferAlreadyFinalized,
    #[msg("stage_finalize digest does not match the staged schedule bytes")]
    BufferDigestMismatch,
    #[msg("stage_write offset/length is out of the buffer's bounds")]
    BufferOutOfRange,
    #[msg("schedule buffer account is not the canonical PDA for its own recorded rent payer")]
    BufferSeedsMismatch,
    #[msg("staged schedule bytes failed to decode as a canonical preimage")]
    InvalidScheduleBytes,
    #[msg("staged schedule length exceeds the 512-period cap")]
    BufferTooLarge,
    #[msg("mint carries a token extension outside the launch allowlist")]
    MintExtensionForbidden,
    #[msg("mint account data is not a readable Token/Token-2022 mint")]
    InvalidMint,
    #[msg("bitmap allocation does not match the recorded leaf count")]
    BitmapSizeMismatch,
    #[msg("vault and treasury must not be the same token account")]
    InvalidTreasury,
    #[msg("beneficiary must not be the zero public key")]
    InvalidBeneficiary,
    #[msg("stored schedule no longer passes validation")]
    InvalidStoredSchedule,
    #[msg("recovered public key does not hash to the claimed address")]
    IdentityMismatch,
    #[msg("vault is frozen or carries a delegate or close authority")]
    VaultNotClean,
    #[msg("vault holds no excess above the claimable total")]
    VaultExcessZero,
    #[msg("token_program is not Token-2022 or does not own the mint")]
    TokenProgramMismatch,
}
