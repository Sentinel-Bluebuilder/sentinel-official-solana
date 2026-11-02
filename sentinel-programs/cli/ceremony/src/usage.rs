use crate::CeremonyError;
use crate::exit_code;

#[derive(Debug)]
pub struct UsageError(anyhow::Error);

impl std::fmt::Display for UsageError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}", self.0)
    }
}

impl std::error::Error for UsageError {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        self.0.source()
    }
}

pub trait TagUsage<T> {
    fn tag_usage(self) -> anyhow::Result<T>;
}

impl<T> TagUsage<T> for anyhow::Result<T> {
    fn tag_usage(self) -> anyhow::Result<T> {
        self.map_err(|error| anyhow::Error::new(UsageError(error)))
    }
}

pub fn usage_error(msg: impl std::fmt::Display) -> anyhow::Error {
    anyhow::Error::new(UsageError(anyhow::anyhow!("{msg}")))
}

pub fn exit_code_for(err: &anyhow::Error) -> i32 {
    if err.chain().any(|cause| cause.is::<UsageError>()) {
        return exit_code::USAGE;
    }
    if let Some(ceremony_error) = err
        .chain()
        .find_map(|cause| cause.downcast_ref::<CeremonyError>())
    {
        return if ceremony_error.is_transient() {
            exit_code::TRANSIENT
        } else {
            exit_code::GATE_REFUSED
        };
    }
    let transient = err
        .chain()
        .any(|cause| cause.is::<solana_client::client_error::ClientError>());
    if transient {
        return exit_code::TRANSIENT;
    }
    exit_code::GATE_REFUSED
}

#[cfg(test)]
#[allow(
    unused_imports,
    unused_qualifications,
    clippy::unwrap_used,
    clippy::expect_used,
    clippy::panic,
    clippy::indexing_slicing,
    clippy::arithmetic_side_effects
)]
mod tests {
    use super::*;
    use crate::CeremonyError;
    use crate::exit_code;
    use crate::runner::{CeremonySigners, HaltedStep, StepRecord, run_steps};
    use crate::sender::{AccountReader, RpcSender, Sender, Transport};
    use crate::steps::{CeremonyAddresses, CeremonyInputs, RentTable, Step, build_ceremony_steps};
    use crate::usage::exit_code_for;
    use anyhow::Context as _;
    use solana_instruction::Instruction;
    use solana_keypair::Keypair;
    use solana_pubkey::Pubkey;
    use solana_signature::Signature;
    use solana_signer::Signer;
    use solana_transaction::versioned::VersionedTransaction;
    use txv1::{TxConfig, build_signed};
    #[test]
    fn gate_refused_transient_and_usage_get_distinct_exit_codes() {
        let gate = anyhow::anyhow!("refusing: soak window not yet satisfied");
        assert_eq!(exit_code_for(&gate), exit_code::GATE_REFUSED);

        let transient = anyhow::Error::new(HaltedStep {
            step: "distributor_mint_supply".into(),
            source: CeremonyError::TransportFailed {
                signature: Signature::new_unique(),
                message: "confirmation timed out".into(),
            },
            signature: None,
            records: vec![],
        });
        assert_eq!(exit_code_for(&transient), exit_code::TRANSIENT);

        let usage =
            usage_error("--other-rpc-url is required unless --allow-unknown-genesis is set");
        assert_eq!(exit_code_for(&usage), exit_code::USAGE);

        let wrapped_usage = usage.context("while loading --keys-dir");
        assert_eq!(exit_code_for(&wrapped_usage), exit_code::USAGE);
        let wrapped_transient = transient.context("while running the ceremony");
        assert_eq!(exit_code_for(&wrapped_transient), exit_code::TRANSIENT);
    }

    #[test]
    fn halted_step_with_a_deterministic_cause_lands_gate_refused_not_transient() {
        let halted = anyhow::Error::new(HaltedStep {
            step: "distributor_mint_supply".into(),
            source: CeremonyError::MissingSigner {
                address: Pubkey::new_unique(),
            },
            signature: None,
            records: vec![],
        });
        assert_eq!(exit_code_for(&halted), exit_code::GATE_REFUSED);

        let wrapped = halted.context("while running the ceremony");
        assert_eq!(exit_code_for(&wrapped), exit_code::GATE_REFUSED);
    }
}
