use solana_sdk::program_error::ProgramError;

#[derive(Clone, Debug, Eq, PartialEq, thiserror::Error)]
#[non_exhaustive]
pub enum MintError {
    #[error("failed to build a Token-2022 instruction: {0}")]
    InstructionBuildFailed(#[from] ProgramError),
}
