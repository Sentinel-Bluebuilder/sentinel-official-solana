use crate::error::CeremonyError;
use crate::sender::Sender;
use crate::steps::Step;
use anyhow::Context;
use solana_keypair::Keypair;
use solana_pubkey::Pubkey;
use solana_signature::Signature;
use solana_signer::Signer;
use std::path::Path;
use std::sync::Arc;

#[derive(Debug)]
pub struct HaltedStep {
    pub step: String,
    pub source: CeremonyError,
    pub signature: Option<Signature>,
    pub records: Vec<StepRecord>,
}

impl std::fmt::Display for HaltedStep {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(
            f,
            "ceremony halted at step '{}': {}",
            self.step, self.source
        )
    }
}

impl From<HaltedStep> for String {
    fn from(error: HaltedStep) -> String {
        error.to_string()
    }
}

impl std::error::Error for HaltedStep {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        Some(&self.source)
    }
}

pub struct CeremonySigners {
    pub upgrade_authority: Option<Keypair>,
    pub payer: Arc<Keypair>,
    pub mint: Keypair,
    pub treasury: Keypair,
    pub vault: Keypair,
}

pub fn resolve_signers<'a>(
    signers: &'a CeremonySigners,
    extra_signers: &[Pubkey],
) -> Result<Vec<&'a Keypair>, CeremonyError> {
    let mut candidates: Vec<&Keypair> = vec![
        &signers.payer,
        &signers.mint,
        &signers.treasury,
        &signers.vault,
    ];
    if let Some(upgrade_authority) = &signers.upgrade_authority {
        candidates.push(upgrade_authority);
    }
    extra_signers
        .iter()
        .map(|public_key| {
            candidates
                .iter()
                .copied()
                .find(|keypair| keypair.pubkey() == *public_key)
                .ok_or(CeremonyError::MissingSigner {
                    address: *public_key,
                })
        })
        .collect()
}

#[derive(Debug, Clone)]
pub struct StepRecord {
    pub step: String,
    pub signature: Option<Signature>,
    pub slot: Option<u64>,
    pub skipped: bool,
}

#[allow(clippy::result_large_err)]
pub fn run_steps(
    sender: &mut dyn Sender,
    steps: &[Step],
    signers: &CeremonySigners,
) -> Result<Vec<StepRecord>, HaltedStep> {
    let mut records = Vec::with_capacity(steps.len());
    for step in steps {
        if sender.is_step_done(step) {
            eprintln!("skip (already done): {}", step.name);
            records.push(StepRecord {
                step: step.name.clone(),
                signature: None,
                slot: None,
                skipped: true,
            });
            continue;
        }
        let signer_kps = match resolve_signers(signers, &step.extra_signers) {
            Ok(kps) => kps,
            Err(error) => {
                return Err(HaltedStep {
                    step: step.name.clone(),
                    source: error,
                    signature: None,
                    records,
                });
            }
        };
        match sender.send(&step.name, &step.ixs, &signer_kps) {
            Ok(signature) => {
                eprintln!("ok: {} ({signature})", step.name);
                records.push(StepRecord {
                    step: step.name.clone(),
                    signature: Some(signature),
                    slot: None,
                    skipped: false,
                });
            }
            Err(error) => {
                let signature = error.signature();
                if signature.is_some() {
                    records.push(StepRecord {
                        step: step.name.clone(),
                        signature,
                        slot: None,
                        skipped: false,
                    });
                }
                return Err(HaltedStep {
                    step: step.name.clone(),
                    source: error,
                    signature,
                    records,
                });
            }
        }
    }
    Ok(records)
}

pub fn write_file_fsync(path: &Path, bytes: &[u8]) -> anyhow::Result<()> {
    use std::io::Write as _;
    let mut file = std::fs::File::create(path)
        .with_context(|| format!("failed to create {}", path.display()))?;
    file.write_all(bytes)
        .with_context(|| format!("failed to write {}", path.display()))?;
    file.sync_all()
        .with_context(|| format!("failed to fsync {}", path.display()))
}

#[cfg(test)]
#[allow(clippy::expect_used, clippy::indexing_slicing)]
mod tests {
    use super::*;
    use solana_instruction::Instruction;

    #[test]
    fn write_file_fsync_round_trips_and_replaces() {
        let dir = tempfile::tempdir().expect("tempdir");
        let path = dir.path().join("state.json");
        write_file_fsync(&path, b"longer-original-content").expect("first write");
        write_file_fsync(&path, b"short").expect("second write");
        assert_eq!(std::fs::read(&path).expect("read back"), b"short");
    }

    fn step(name: &str) -> Step {
        Step {
            name: name.to_string(),
            ixs: vec![],
            extra_signers: vec![],
            resume: None,
        }
    }

    fn signers() -> CeremonySigners {
        CeremonySigners {
            upgrade_authority: Some(Keypair::new()),
            payer: Arc::new(Keypair::new()),
            mint: Keypair::new(),
            treasury: Keypair::new(),
            vault: Keypair::new(),
        }
    }

    struct FakeSender {
        fail_at: &'static str,
        fail_signature: Signature,
        message: &'static str,
    }
    impl Sender for FakeSender {
        fn send(
            &mut self,
            step_name: &str,
            _ixs: &[Instruction],
            _signers: &[&Keypair],
        ) -> Result<Signature, CeremonyError> {
            if step_name == self.fail_at {
                return Err(CeremonyError::TransportFailed {
                    signature: self.fail_signature,
                    message: self.message.into(),
                });
            }
            Ok(Signature::new_unique())
        }
        fn is_step_done(&self, _step: &Step) -> bool {
            false
        }
    }

    #[test]
    fn run_steps_preserves_landed_records_and_the_halted_steps_signature_on_a_confirm_failure() {
        let steps = vec![step("a"), step("b"), step("c")];
        let fail_signature = Signature::new_unique();
        let mut sender = FakeSender {
            fail_at: "b",
            fail_signature,
            message: "confirmation timed out",
        };

        let halted = run_steps(&mut sender, &steps, &signers())
            .expect_err("step b's confirm failure must halt the run");

        assert_eq!(halted.step, "b");
        assert_eq!(halted.signature, Some(fail_signature));

        assert_eq!(halted.records.len(), 2);
        assert_eq!(halted.records[0].step, "a");
        assert!(!halted.records[0].skipped);
        assert!(halted.records[0].signature.is_some());
        assert_eq!(halted.records[1].step, "b");
        assert_eq!(halted.records[1].signature, Some(fail_signature));

        assert!(matches!(
            halted.source,
            CeremonyError::TransportFailed { .. }
        ));
        assert!(halted.source.is_transient());
    }

    #[test]
    fn run_steps_halted_step_source_is_typed_and_classifies_a_deterministic_halt_correctly() {
        let mut unresolvable = step("needs_extra_signer");
        unresolvable.extra_signers = vec![Pubkey::new_unique()];
        let steps = vec![unresolvable];
        let mut sender = FakeSender {
            fail_at: "never reached",
            fail_signature: Signature::new_unique(),
            message: "confirmation timed out",
        };

        let halted = run_steps(&mut sender, &steps, &signers())
            .expect_err("an unresolvable extra signer address must halt before any send");

        assert_eq!(halted.step, "needs_extra_signer");
        assert!(matches!(halted.source, CeremonyError::MissingSigner { .. }));
        assert!(
            !halted.source.is_transient(),
            "a missing local keypair is a deterministic construction bug, not a retriable \
             transport hiccup"
        );
    }
}
