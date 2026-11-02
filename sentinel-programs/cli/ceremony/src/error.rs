use solana_client::client_error::ClientError;
use solana_pubkey::Pubkey;
use solana_signature::Signature;
use solana_signer::SignerError;
use thiserror::Error;
use txv1::TxError;

#[derive(Debug, Error)]
#[non_exhaustive]
pub enum CeremonyError {
    #[error("transaction exceeds the {limit_bytes}-byte wire limit: {size_bytes} bytes")]
    WireLimitExceeded {
        limit_bytes: usize,
        size_bytes: usize,
    },
    #[error("failed to fetch a recent blockhash: {0}")]
    BlockhashFetchFailed(#[source] ClientError),
    #[error("transaction {signature} failed: {source}")]
    TransactionFailed {
        signature: Signature,
        #[source]
        source: ClientError,
    },
    #[error("transaction {signature} failed: {message}")]
    TransportFailed {
        signature: Signature,
        message: String,
    },
    #[error("failed to build v1 message: {0}")]
    CompileFailed(#[source] TxError),
    #[error("failed to sign v1 transaction: {0}")]
    SignFailed(#[source] SignerError),
    #[error("transaction simulation failed: {0}")]
    SimulationFailed(#[source] ClientError),
    #[error("extra signer {address} has no local keypair")]
    MissingSigner { address: Pubkey },
    #[error("{step}: {source}")]
    StepFailed {
        step: String,
        #[source]
        source: Box<CeremonyError>,
    },
    #[error("tx-v1 feature inactive on cluster {cluster_hint}")]
    PreflightFeatureInactive { cluster_hint: String },
    #[error("RPC node cannot decode v1 transactions: {message}")]
    PreflightRpcCannotDecodeV1 { message: String },
    #[error("failed to build the tx-v1 preflight probe: {0}")]
    PreflightProbeBuildFailed(#[source] TxError),
    #[error("failed to preflight tx-v1 support: {0}")]
    PreflightRpcFailed(#[source] ClientError),
}

impl From<TxError> for CeremonyError {
    fn from(error: TxError) -> Self {
        match error {
            TxError::SignFailed(source) => Self::SignFailed(source),
            other => Self::CompileFailed(other),
        }
    }
}

impl From<txv1::preflight::PreflightError> for CeremonyError {
    fn from(error: txv1::preflight::PreflightError) -> Self {
        use txv1::preflight::PreflightError as Preflight;
        match error {
            Preflight::FeatureInactive { cluster_hint } => {
                Self::PreflightFeatureInactive { cluster_hint }
            }
            Preflight::RpcCannotDecodeV1 { message } => {
                Self::PreflightRpcCannotDecodeV1 { message }
            }
            Preflight::ProbeBuildFailed(source) => Self::PreflightProbeBuildFailed(source),
            Preflight::RpcFailed(source) => Self::PreflightRpcFailed(source),
        }
    }
}

impl CeremonyError {
    pub(crate) fn with_step(step: impl Into<String>, source: CeremonyError) -> Self {
        Self::StepFailed {
            step: step.into(),
            source: Box::new(source),
        }
    }

    #[must_use]
    pub fn signature(&self) -> Option<Signature> {
        match self {
            Self::TransactionFailed { signature, .. } | Self::TransportFailed { signature, .. } => {
                Some(*signature)
            }
            Self::StepFailed { source, .. } => source.signature(),
            _ => None,
        }
    }

    #[must_use]
    pub fn is_transient(&self) -> bool {
        match self {
            Self::BlockhashFetchFailed(_)
            | Self::TransactionFailed { .. }
            | Self::TransportFailed { .. } => true,
            Self::WireLimitExceeded { .. }
            | Self::CompileFailed(_)
            | Self::SignFailed(_)
            | Self::SimulationFailed(_)
            | Self::MissingSigner { .. }
            | Self::PreflightFeatureInactive { .. }
            | Self::PreflightRpcCannotDecodeV1 { .. }
            | Self::PreflightProbeBuildFailed(_)
            | Self::PreflightRpcFailed(_) => false,
            Self::StepFailed { source, .. } => source.is_transient(),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn is_transient_recurses_through_step_wrapping() {
        let gate = CeremonyError::WireLimitExceeded {
            limit_bytes: 4096,
            size_bytes: 5000,
        };
        assert!(!gate.is_transient());
        let wrapped_gate = CeremonyError::with_step("distributor_initialize_config", gate);
        assert!(!wrapped_gate.is_transient());

        let transient = CeremonyError::TransportFailed {
            signature: Signature::default(),
            message: "confirm timed out".into(),
        };
        assert!(transient.is_transient());
        let wrapped_transient =
            CeremonyError::with_step("distributor_initialize_metadata:execute", transient);
        assert!(wrapped_transient.is_transient());

        let doubly_wrapped =
            CeremonyError::with_step("distributor_initialize_metadata", wrapped_transient);
        assert!(doubly_wrapped.is_transient());
    }
}
