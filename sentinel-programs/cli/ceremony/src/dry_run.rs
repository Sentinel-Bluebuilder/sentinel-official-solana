use crate::CeremonyError;
use crate::sender::{AccountReader, Transport};
use base64::{Engine as _, engine::general_purpose::STANDARD};
use solana_pubkey::Pubkey;
use solana_signature::Signature;
use solana_transaction::versioned::VersionedTransaction;

pub struct DryRunTransport<T>(pub T);

impl<T: AccountReader> AccountReader for DryRunTransport<T> {
    fn account_exists(&self, address: &Pubkey) -> bool {
        self.0.account_exists(address)
    }
    fn account_data(&self, address: &Pubkey) -> Option<Vec<u8>> {
        self.0.account_data(address)
    }
}

impl<T: Transport> Transport for DryRunTransport<T> {
    fn latest_blockhash(&self) -> Result<solana_hash::Hash, CeremonyError> {
        Ok(solana_hash::Hash::default())
    }

    fn submit(&mut self, transaction: &VersionedTransaction) -> Result<Signature, CeremonyError> {
        print_dry_run_tx(None, transaction);
        Ok(transaction.signatures.first().copied().unwrap_or_default())
    }

    fn simulate_compute_units(
        &self,
        transaction: &VersionedTransaction,
    ) -> Result<Option<u64>, CeremonyError> {
        self.0.simulate_compute_units(transaction)
    }

    fn can_simulate(&self) -> bool {
        self.0.can_simulate()
    }
}

fn print_dry_run_tx<T: serde::Serialize + FirstSignature>(step: Option<&str>, transaction: &T) {
    #[allow(clippy::expect_used)]
    let bytes = bincode::serialize(transaction).expect("a signed transaction always serializes");
    let mut record = serde_json::json!({
        "dry_run": true,
        "signature": transaction.first_signature().map(|signature| signature.to_string()),
        "tx_base64": STANDARD.encode(bytes),
    });
    if let (Some(step), Some(obj)) = (step, record.as_object_mut()) {
        obj.insert(
            "step".to_string(),
            serde_json::Value::String(step.to_string()),
        );
    }
    println!("{record}");
}

trait FirstSignature {
    fn first_signature(&self) -> Option<Signature>;
}

impl FirstSignature for VersionedTransaction {
    fn first_signature(&self) -> Option<Signature> {
        self.signatures.first().copied()
    }
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
    use crate::persist::step_record_json;
    use crate::runner::{CeremonySigners, HaltedStep, StepRecord, run_steps};
    use crate::sender::{AccountReader, RpcSender, Sender, Transport};
    use crate::sender::{MAX_CU_LIMIT, MAX_LOADED_ACCOUNTS_DATA_BYTES};
    use crate::steps::{CeremonyAddresses, CeremonyInputs, RentTable, Step, build_ceremony_steps};
    use crate::usage::exit_code_for;
    use anyhow::Context as _;
    use solana_instruction::Instruction;
    use solana_keypair::Keypair;
    use solana_pubkey::Pubkey;
    use solana_signature::Signature;
    use solana_signer::Signer;
    use solana_transaction::versioned::VersionedTransaction;
    use std::sync::Arc;
    use txv1::{TxConfig, build_signed};
    #[test]
    fn dry_run_transport_never_calls_the_inner_transports_submit() {
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
            fn latest_blockhash(&self) -> Result<solana_hash::Hash, CeremonyError> {
                Ok(solana_hash::Hash::default())
            }
            fn submit(&mut self, _tx: &VersionedTransaction) -> Result<Signature, CeremonyError> {
                panic!("--dry-run must never reach the inner transport's submit");
            }
        }

        let payer = Keypair::new();
        let upgrade_authority = Keypair::new();
        let mut sender = RpcSender::new(
            DryRunTransport(FakeTransport),
            &payer,
            Some(&upgrade_authority),
            0,
        );
        let instruction = Instruction {
            program_id: Pubkey::new_unique(),
            accounts: vec![],
            data: vec![],
        };
        let signature = sender
            .send("test_step", std::slice::from_ref(&instruction), &[])
            .expect("dry-run's own submit always succeeds locally, with no network call");
        assert_ne!(
            signature,
            Signature::default(),
            "the printed signature is a real one computed by local signing, not a placeholder"
        );
    }

    #[test]
    fn dry_run_signed_tx_commits_to_the_default_blockhash_so_it_cannot_broadcast() {
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
            fn latest_blockhash(&self) -> Result<solana_hash::Hash, CeremonyError> {
                Ok(solana_hash::Hash::new_unique())
            }
            fn submit(&mut self, _tx: &VersionedTransaction) -> Result<Signature, CeremonyError> {
                panic!("--dry-run must never reach the inner transport's submit");
            }
        }
        let payer = Keypair::new();
        let instruction = Instruction {
            program_id: Pubkey::new_unique(),
            accounts: vec![],
            data: vec![1, 2, 3],
        };
        let mut sender = RpcSender::new(DryRunTransport(FakeTransport), &payer, None, 0);
        let signature = sender
            .send("probe", std::slice::from_ref(&instruction), &[])
            .expect("dry-run send is local-only");

        let tx_config = TxConfig::new(MAX_CU_LIMIT, MAX_LOADED_ACCOUNTS_DATA_BYTES);
        let expected = build_signed(
            &payer.pubkey(),
            std::slice::from_ref(&instruction),
            &[&payer],
            solana_hash::Hash::default(),
            &tx_config,
        )
        .expect("builds");
        assert_eq!(
            Some(signature),
            expected.signatures.first().copied(),
            "the dry-run signature must be over a message carrying Hash::default(), \
             not the inner transport's live blockhash"
        );
    }

    #[test]
    fn dry_run_run_steps_return_value_carries_every_genesis_record_for_the_json_stream() {
        struct FakeTransport {
            submit_calls: usize,
        }
        impl AccountReader for FakeTransport {
            fn account_exists(&self, _address: &Pubkey) -> bool {
                false
            }
            fn account_data(&self, _address: &Pubkey) -> Option<Vec<u8>> {
                None
            }
        }
        impl Transport for FakeTransport {
            fn latest_blockhash(&self) -> Result<solana_hash::Hash, CeremonyError> {
                Ok(solana_hash::Hash::default())
            }
            fn submit(&mut self, _tx: &VersionedTransaction) -> Result<Signature, CeremonyError> {
                self.submit_calls += 1;
                Ok(Signature::default())
            }
        }

        let payer = Keypair::new();
        let upgrade_authority = Keypair::new();
        let signers = CeremonySigners {
            upgrade_authority: Some(Keypair::new()),
            payer: Arc::new(Keypair::new()),
            mint: Keypair::new(),
            treasury: Keypair::new(),
            vault: Keypair::new(),
        };
        let genesis_steps = vec![
            Step {
                name: "distributor_create_mint".into(),
                ixs: vec![],
                extra_signers: vec![],
                resume: None,
            },
            Step {
                name: "distributor_create_treasury".into(),
                ixs: vec![],
                extra_signers: vec![],
                resume: None,
            },
        ];
        let mut sender = RpcSender::new(
            DryRunTransport(FakeTransport { submit_calls: 0 }),
            &payer,
            Some(&upgrade_authority),
            0,
        );

        let records = run_steps(&mut sender, &genesis_steps, &signers)
            .expect("dry-run's own submit always succeeds locally");

        assert_eq!(
            sender.transport.0.submit_calls, 0,
            "--dry-run must never reach the inner transport's submit"
        );
        assert_eq!(
            records.len(),
            2,
            "one record per genesis step must survive, not an empty Vec"
        );
        assert_eq!(records[0].step, "distributor_create_mint");
        assert_eq!(records[1].step, "distributor_create_treasury");
        assert!(
            records
                .iter()
                .all(|record| !record.skipped && record.signature.is_some()),
            "every step actually ran (not skipped) and carries the signature \
             `--json`'s stream would print: {records:?}"
        );

        for record in &records {
            let json = step_record_json(record);
            assert_eq!(json["step"], record.step.as_str());
            assert!(json["signature"].is_string());
        }
    }
}
