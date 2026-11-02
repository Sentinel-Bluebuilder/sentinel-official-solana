use crate::error::CeremonyError;
use crate::steps::Step;
use sha2::{Digest, Sha256};
use solana_client::rpc_client::RpcClient;
use solana_client::rpc_config::RpcSimulateTransactionConfig;
use solana_commitment_config::CommitmentConfig;
use solana_hash::Hash;
use solana_instruction::Instruction;
use solana_keypair::Keypair;
use solana_message::VersionedMessage;
use solana_pubkey::Pubkey;
use solana_signature::Signature;
use solana_signer::Signer;
use solana_transaction::versioned::VersionedTransaction;
use std::collections::HashMap;
use txv1::{TxConfig, build_signed, compile, priority_fee_lamports};

pub trait Sender {
    fn send(
        &mut self,
        step_name: &str,
        ixs: &[Instruction],
        signers: &[&Keypair],
    ) -> Result<Signature, CeremonyError>;

    fn is_step_done(&self, step: &Step) -> bool;
}

pub trait AccountReader {
    fn account_exists(&self, address: &Pubkey) -> bool;
    fn account_data(&self, address: &Pubkey) -> Option<Vec<u8>>;
}

pub trait Transport: AccountReader {
    fn latest_blockhash(&self) -> Result<Hash, CeremonyError>;

    fn submit(&mut self, transaction: &VersionedTransaction) -> Result<Signature, CeremonyError>;

    fn simulate_compute_units(
        &self,
        _tx: &VersionedTransaction,
    ) -> Result<Option<u64>, CeremonyError> {
        Ok(None)
    }

    fn can_simulate(&self) -> bool {
        false
    }
}

pub struct RpcTransport {
    inner: RpcClient,
    commitment: CommitmentConfig,
}

impl RpcTransport {
    #[must_use]
    pub fn new(url: String, commitment: CommitmentConfig) -> Self {
        Self {
            inner: RpcClient::new_with_commitment(url, commitment),
            commitment,
        }
    }
}

impl AccountReader for RpcTransport {
    fn account_exists(&self, address: &Pubkey) -> bool {
        self.inner
            .get_account_with_commitment(address, self.commitment)
            .map(|response| response.value.is_some())
            .unwrap_or(false)
    }

    fn account_data(&self, address: &Pubkey) -> Option<Vec<u8>> {
        self.inner
            .get_account_with_commitment(address, self.commitment)
            .ok()
            .and_then(|response| response.value)
            .map(|account| account.data)
    }
}

impl Transport for RpcTransport {
    fn latest_blockhash(&self) -> Result<Hash, CeremonyError> {
        self.inner
            .get_latest_blockhash()
            .map_err(CeremonyError::BlockhashFetchFailed)
    }

    fn submit(&mut self, transaction: &VersionedTransaction) -> Result<Signature, CeremonyError> {
        let signature = transaction.signatures.first().copied().unwrap_or_default();
        self.inner
            .send_and_confirm_transaction(transaction)
            .map_err(|source| CeremonyError::TransactionFailed { signature, source })
    }

    fn simulate_compute_units(
        &self,
        transaction: &VersionedTransaction,
    ) -> Result<Option<u64>, CeremonyError> {
        rpc_units_consumed(&self.inner, transaction)
    }
    fn can_simulate(&self) -> bool {
        true
    }
}

pub const MAX_CU_LIMIT: u32 = 1_400_000;

pub const MAX_LOADED_ACCOUNTS_DATA_BYTES: u32 = 64 * 1024 * 1024;

fn cu_limit_from_units(units: u64) -> u32 {
    let with_margin = units.saturating_mul(11) / 10;
    u32::try_from(with_margin.min(u64::from(MAX_CU_LIMIT))).unwrap_or(MAX_CU_LIMIT)
}

pub(crate) fn cu_cache_key(ixs: &[Instruction]) -> [u8; 32] {
    let mut hasher = Sha256::new();
    for instruction in ixs {
        hasher.update(instruction.program_id.as_ref());
        hasher.update((instruction.accounts.len() as u64).to_le_bytes());
        for account in &instruction.accounts {
            hasher.update([u8::from(account.is_signer), u8::from(account.is_writable)]);
        }
        hasher.update((instruction.data.len() as u64).to_le_bytes());
        hasher.update(&instruction.data);
    }
    hasher.finalize().into()
}

pub(crate) fn rpc_units_consumed(
    client: &RpcClient,
    transaction: &VersionedTransaction,
) -> Result<Option<u64>, CeremonyError> {
    let config = RpcSimulateTransactionConfig {
        sig_verify: false,
        replace_recent_blockhash: true,
        commitment: Some(CommitmentConfig::confirmed()),
        ..Default::default()
    };
    let response = client
        .simulate_transaction_with_config(transaction, config)
        .map_err(CeremonyError::SimulationFailed)?;
    Ok(response.value.units_consumed)
}

pub(crate) fn simulate_cu_limit(
    payer: &Pubkey,
    ixs: &[Instruction],
    blockhash: Hash,
    can_simulate: bool,
    simulate: impl FnOnce(&VersionedTransaction) -> Result<Option<u64>, CeremonyError>,
) -> Result<u32, CeremonyError> {
    let tx_config = TxConfig::new(MAX_CU_LIMIT, MAX_LOADED_ACCOUNTS_DATA_BYTES);
    let Ok(message) = compile(payer, ixs, blockhash, &tx_config) else {
        return Ok(MAX_CU_LIMIT);
    };
    let num_sigs = message.header.num_required_signatures as usize;
    let transaction = VersionedTransaction {
        signatures: vec![Signature::default(); num_sigs],
        message: VersionedMessage::V1(message),
    };
    match simulate(&transaction)? {
        Some(units) => Ok(cu_limit_from_units(units)),
        None => {
            if can_simulate {
                tracing::warn!("simulation reported no compute units; using the maximum limit");
            } else {
                tracing::debug!("simulation unavailable; using the maximum compute limit");
            }
            Ok(MAX_CU_LIMIT)
        }
    }
}

pub const ROOTS_PER_CHUNK: usize = 115;

#[must_use]
pub fn has_signer(ixs: &[Instruction], address: &Pubkey) -> bool {
    ixs.iter().any(|instruction| {
        instruction
            .accounts
            .iter()
            .any(|account_meta| account_meta.pubkey == *address && account_meta.is_signer)
    })
}

pub struct RpcSender<'a, T: Transport> {
    pub transport: T,
    pub payer: &'a Keypair,
    pub upgrade_authority: Option<&'a Keypair>,
    pub micro_lamports_per_cu: u64,
    cu_limits: HashMap<[u8; 32], u32>,
}

impl<'a, T: Transport> RpcSender<'a, T> {
    #[must_use]
    pub fn new(
        transport: T,
        payer: &'a Keypair,
        upgrade_authority: Option<&'a Keypair>,
        micro_lamports_per_cu: u64,
    ) -> Self {
        Self {
            transport,
            payer,
            upgrade_authority,
            micro_lamports_per_cu,
            cu_limits: HashMap::new(),
        }
    }
}

impl<T: Transport> Sender for RpcSender<'_, T> {
    fn send(
        &mut self,
        step_name: &str,
        ixs: &[Instruction],
        signers: &[&Keypair],
    ) -> Result<Signature, CeremonyError> {
        let blockhash = self
            .transport
            .latest_blockhash()
            .map_err(|error| CeremonyError::with_step(step_name, error))?;
        let payer_pk = self.payer.pubkey();

        let cu_limit = {
            let key = cu_cache_key(ixs);
            match self.cu_limits.get(&key).copied() {
                Some(limit) => limit,
                None => {
                    let limit = simulate_cu_limit(
                        &payer_pk,
                        ixs,
                        blockhash,
                        self.transport.can_simulate(),
                        |transaction| self.transport.simulate_compute_units(transaction),
                    )
                    .map_err(|error| CeremonyError::with_step(step_name, error))?;
                    self.cu_limits.insert(key, limit);
                    limit
                }
            }
        };

        let tx_config =
            TxConfig::new(cu_limit, MAX_LOADED_ACCOUNTS_DATA_BYTES).with_priority_fee_lamports(
                priority_fee_lamports(self.micro_lamports_per_cu, cu_limit),
            );

        crate::wire_size::check_tx_within_limit(ixs, &payer_pk, &tx_config)
            .map_err(|error| CeremonyError::with_step(step_name, error))?;
        let mut all_signers: Vec<&Keypair> = vec![self.payer];
        if let Some(upgrade_authority) = self.upgrade_authority
            && has_signer(ixs, &upgrade_authority.pubkey())
        {
            all_signers.push(upgrade_authority);
        }
        all_signers.extend_from_slice(signers);
        let transaction = build_signed(&payer_pk, ixs, &all_signers, blockhash, &tx_config)
            .map_err(|error| CeremonyError::with_step(step_name, error.into()))?;
        self.transport
            .submit(&transaction)
            .map_err(|error| CeremonyError::with_step(step_name, error))
    }

    fn is_step_done(&self, step: &Step) -> bool {
        crate::predicates::is_step_done(&self.transport, step)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use solana_instruction::AccountMeta;

    #[test]
    fn rpc_transport_keeps_the_requested_commitment() {
        let confirmed = RpcTransport::new(
            "http://127.0.0.1:8899".into(),
            CommitmentConfig::confirmed(),
        );
        assert_eq!(confirmed.commitment, CommitmentConfig::confirmed());
        let finalized = RpcTransport::new(
            "http://127.0.0.1:8899".into(),
            CommitmentConfig::finalized(),
        );
        assert_eq!(finalized.commitment, CommitmentConfig::finalized());
    }

    struct FakeTransport;
    impl AccountReader for FakeTransport {
        fn account_exists(&self, _address: &Pubkey) -> bool {
            false
        }
        fn account_data(&self, _address: &Pubkey) -> Option<Vec<u8>> {
            None
        }
    }
    impl Transport for FakeTransport {
        fn latest_blockhash(&self) -> Result<Hash, CeremonyError> {
            Ok(Hash::default())
        }
        fn submit(&mut self, _tx: &VersionedTransaction) -> Result<Signature, CeremonyError> {
            Ok(Signature::default())
        }
        fn simulate_compute_units(
            &self,
            _tx: &VersionedTransaction,
        ) -> Result<Option<u64>, CeremonyError> {
            Err(CeremonyError::SimulationFailed(
                solana_client::client_error::ClientErrorKind::Custom("sim down".into()).into(),
            ))
        }
        fn can_simulate(&self) -> bool {
            true
        }
    }

    #[test]
    fn send_halts_on_simulation_failure_without_submitting() {
        let payer = Keypair::new();
        let instruction = Instruction {
            program_id: Pubkey::new_unique(),
            accounts: vec![AccountMeta::new(payer.pubkey(), true)],
            data: vec![],
        };
        let mut sender = RpcSender::new(FakeTransport, &payer, None, 0);
        let result = sender.send("probe", std::slice::from_ref(&instruction), &[]);
        assert!(
            matches!(&result, Err(CeremonyError::StepFailed { source, .. })
                if matches!(**source, CeremonyError::SimulationFailed(_))),
            "expected a step-wrapped SimulationFailed, got {result:?}"
        );
        assert!(
            result.is_err_and(|error| !error.is_transient()),
            "SimulationFailed must be non-transient"
        );
    }

    #[test]
    fn has_signer_only_sees_accounts_actually_marked_signer() {
        let upgrade_authority = Pubkey::new_unique();
        let other = Pubkey::new_unique();
        let non_signing = Instruction {
            program_id: Pubkey::new_unique(),
            accounts: vec![
                AccountMeta::new(other, true),
                AccountMeta::new_readonly(upgrade_authority, false),
            ],
            data: vec![],
        };
        assert!(!has_signer(
            std::slice::from_ref(&non_signing),
            &upgrade_authority
        ));
        assert!(has_signer(std::slice::from_ref(&non_signing), &other));

        let signing = Instruction {
            program_id: Pubkey::new_unique(),
            accounts: vec![AccountMeta::new_readonly(upgrade_authority, true)],
            data: vec![],
        };
        assert!(has_signer(&[non_signing, signing], &upgrade_authority));

        assert!(!has_signer(&[], &upgrade_authority));
    }

    fn bitmap_shaped(program: Pubkey, shard_id: u16, leaf_count: u32) -> Vec<Instruction> {
        let mut data = vec![0u8; 8];
        data.extend_from_slice(&shard_id.to_le_bytes());
        data.extend_from_slice(&leaf_count.to_le_bytes());
        vec![Instruction {
            program_id: program,
            accounts: vec![
                AccountMeta::new(Pubkey::new_unique(), true),
                AccountMeta::new(Pubkey::new_unique(), false),
            ],
            data,
        }]
    }

    #[test]
    fn cu_cache_key_separates_bitmaps_by_allocation_size() {
        let program = Pubkey::new_unique();
        assert_ne!(
            cu_cache_key(&bitmap_shaped(program, 7, 0)),
            cu_cache_key(&bitmap_shaped(program, 7, 65_536)),
            "bitmaps with different leaf_count must not share a CU cache entry"
        );
        assert_eq!(
            cu_cache_key(&bitmap_shaped(program, 7, 1_000)),
            cu_cache_key(&bitmap_shaped(program, 7, 1_000)),
            "byte-identical instructions must share (addresses must not fragment the key)"
        );
    }
}
