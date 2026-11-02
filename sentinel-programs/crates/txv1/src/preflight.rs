use solana_account::Account;
use solana_client::client_error::{ClientError, ClientErrorKind};
use solana_client::rpc_client::RpcClient;
use solana_client::rpc_config::{RpcSimulateTransactionConfig, UiTransactionEncoding};
use solana_client::rpc_request::{RpcError, RpcResponseErrorData};
use solana_client::rpc_response::RpcSimulateTransactionResult;
use solana_commitment_config::CommitmentConfig;
use solana_hash::Hash;
use solana_message::VersionedMessage;
use solana_pubkey::Pubkey;
use solana_signature::Signature;
use solana_system_interface::instruction::transfer;
use solana_transaction::versioned::VersionedTransaction;

use crate::{TxConfig, compile};

#[allow(unreachable_pub)]
mod feature {
    solana_pubkey::declare_id!("txv1aq4pp281K9um3tnPgkfX8UqtFT6wcVW3hNezGLL");
}

pub const ENABLE_TX_V1: Pubkey = feature::ID;

#[derive(Debug, thiserror::Error)]
pub enum PreflightError {
    #[error("tx-v1 feature inactive on this cluster ({cluster_hint})")]
    FeatureInactive { cluster_hint: String },
    #[error("RPC cannot decode v1 transactions: {message}")]
    RpcCannotDecodeV1 { message: String },
    #[error("failed to build v1 probe transaction: {0}")]
    ProbeBuildFailed(#[from] crate::TxError),
    #[error(transparent)]
    RpcFailed(#[from] ClientError),
}

#[must_use]
pub fn is_feature_active(account: Option<&Account>) -> bool {
    account
        .and_then(solana_feature_gate_interface::from_account)
        .is_some_and(|feature| feature.activated_at.is_some())
}

pub fn require_tx_v1(client: &RpcClient, payer: &Pubkey) -> Result<(), PreflightError> {
    let account = client
        .get_account_with_commitment(&ENABLE_TX_V1, CommitmentConfig::finalized())?
        .value;
    if !is_feature_active(account.as_ref()) {
        return Err(PreflightError::FeatureInactive {
            cluster_hint: client.url(),
        });
    }

    let probe = probe_transaction(payer)?;
    let config = RpcSimulateTransactionConfig {
        sig_verify: false,
        replace_recent_blockhash: true,
        encoding: Some(UiTransactionEncoding::Base64),
        ..Default::default()
    };
    classify_probe_result(
        client
            .simulate_transaction_with_config(&probe, config)
            .map(|response| response.value),
    )?;

    if let Ok(version) = client.get_version() {
        tracing::info!(version = %version.solana_core, "RPC version (advisory)");
    }
    Ok(())
}

fn probe_transaction(payer: &Pubkey) -> Result<VersionedTransaction, PreflightError> {
    let message = compile(
        payer,
        &[transfer(payer, payer, 0)],
        Hash::default(),
        &TxConfig::new(1, 64 * 1024 * 1024),
    )?;
    let signatures =
        vec![Signature::default(); usize::from(message.header.num_required_signatures)];
    Ok(VersionedTransaction {
        signatures,
        message: VersionedMessage::V1(message),
    })
}

fn classify_probe_result(
    result: Result<RpcSimulateTransactionResult, ClientError>,
) -> Result<(), PreflightError> {
    let error = match result {
        Ok(_) => return Ok(()),
        Err(error) => error,
    };
    let decoded_v1 = matches!(
        error.kind(),
        ClientErrorKind::RpcError(RpcError::RpcResponseError {
            data: RpcResponseErrorData::SendTransactionPreflightFailure(_),
            ..
        })
    );
    if decoded_v1 {
        return Ok(());
    }
    if let Some(message) = decode_failure_message(&error) {
        return Err(PreflightError::RpcCannotDecodeV1 { message });
    }
    Err(PreflightError::RpcFailed(error))
}

fn decode_failure_message(error: &ClientError) -> Option<String> {
    match error.kind() {
        ClientErrorKind::RpcError(RpcError::RpcResponseError {
            data: RpcResponseErrorData::Empty,
            message,
            ..
        }) => Some(message.clone()),
        ClientErrorKind::Reqwest(_)
        | ClientErrorKind::SerdeJson(_)
        | ClientErrorKind::RpcError(
            RpcError::RpcRequestError(_) | RpcError::ParseError(_) | RpcError::ForUser(_),
        ) => Some(error.to_string()),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    #![allow(
        clippy::unwrap_used,
        clippy::expect_used,
        clippy::panic,
        clippy::indexing_slicing,
        clippy::arithmetic_side_effects
    )]
    use super::*;
    use base64::prelude::{BASE64_STANDARD, Engine as _};
    use serde_json::{Value, json};
    use solana_client::rpc_request::RpcRequest;
    use solana_feature_gate_interface::{Feature, create_account};
    use std::collections::HashMap;

    fn account_of(activated_at: Option<u64>) -> Account {
        create_account(&Feature { activated_at }, 1).into()
    }

    fn account_info_value(account: &Account) -> Value {
        json!({
            "context": {"slot": 1, "apiVersion": null},
            "value": {
                "lamports": account.lamports,
                "data": [BASE64_STANDARD.encode(&account.data), "base64"],
                "owner": account.owner.to_string(),
                "executable": account.executable,
                "rentEpoch": account.rent_epoch,
                "space": account.data.len(),
            }
        })
    }

    fn client_with(mocks: HashMap<RpcRequest, Value>) -> RpcClient {
        RpcClient::new_mock_with_mocks("succeeds".to_string(), mocks)
    }

    #[test]
    fn require_tx_v1_errors_when_feature_inactive() {
        let mut mocks = HashMap::new();
        mocks.insert(
            RpcRequest::GetAccountInfo,
            account_info_value(&account_of(None)),
        );
        let err = require_tx_v1(&client_with(mocks), &Pubkey::new_unique()).unwrap_err();
        assert!(
            matches!(err, PreflightError::FeatureInactive { .. }),
            "got {err:?}"
        );
    }

    #[test]
    fn require_tx_v1_errors_when_node_cannot_decode_v1() {
        let mut mocks = HashMap::new();
        mocks.insert(
            RpcRequest::GetAccountInfo,
            account_info_value(&account_of(Some(1))),
        );
        mocks.insert(RpcRequest::SimulateTransaction, json!("unparseable"));
        let err = require_tx_v1(&client_with(mocks), &Pubkey::new_unique()).unwrap_err();
        assert!(
            matches!(err, PreflightError::RpcCannotDecodeV1 { .. }),
            "got {err:?}"
        );
    }

    #[test]
    fn require_tx_v1_ok_when_node_decodes_v1() {
        let mut default_simulate = HashMap::new();
        default_simulate.insert(
            RpcRequest::GetAccountInfo,
            account_info_value(&account_of(Some(1))),
        );
        assert!(require_tx_v1(&client_with(default_simulate), &Pubkey::new_unique()).is_ok());

        let mut tx_error_simulate = HashMap::new();
        tx_error_simulate.insert(
            RpcRequest::GetAccountInfo,
            account_info_value(&account_of(Some(1))),
        );
        tx_error_simulate.insert(
            RpcRequest::SimulateTransaction,
            json!({"context": {"slot": 1, "apiVersion": null}, "value": {"err": "AccountInUse"}}),
        );
        assert!(require_tx_v1(&client_with(tx_error_simulate), &Pubkey::new_unique()).is_ok());
    }
}
