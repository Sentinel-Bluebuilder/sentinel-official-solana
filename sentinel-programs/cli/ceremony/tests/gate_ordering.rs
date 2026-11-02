use base64::Engine as _;
use base64::engine::general_purpose::STANDARD;
use distributor_dataset::{Attestation, ClaimEntry, ShardDataset};
use solana_pubkey::Pubkey;
use solana_sdk::signature::Keypair;
use std::collections::HashMap;
use std::io::{BufRead, BufReader, Read, Write};
use std::net::{TcpListener, TcpStream};
use std::path::{Path, PathBuf};
use std::process::Command;
use std::sync::{Arc, Mutex};

#[test]
fn dvpn_phase_steps_run_treasury_vault_initialize_config_unpause() {
    let names = ceremony::dvpn_phase::dvpn_phase_step_names();
    let idx = |wanted: &str| names.iter().position(|name| *name == wanted).unwrap();
    assert!(idx("dvpn_create_fee_treasury") < idx("dvpn_create_vault"));
    assert!(idx("dvpn_create_vault") < idx("dvpn_initialize_config"));
    assert!(idx("dvpn_initialize_config") < idx("dvpn_unpause"));

    assert!(!names.contains(&"deploy_dvpn"));
    assert!(!names.contains(&"set_dvpn_upgrade_authority"));
}

#[test]
fn dvpn_unpause_pins_sha512_feature_id_and_is_terminal() {
    assert_eq!(
        ceremony::dvpn_preconditions::SHA512_FEATURE_ID.to_string(),
        "s512oDwgx8hjMnaQjXfqqrZroVj4HvC6TkN3iSSWXCh"
    );
    let step =
        ceremony::dvpn_steps::build_dvpn_unpause_step(&Pubkey::new_unique(), &Pubkey::new_unique());
    assert_eq!(step.name, "dvpn_unpause");
    assert_eq!(
        *ceremony::dvpn_phase::dvpn_phase_step_names()
            .last()
            .unwrap(),
        "dvpn_unpause"
    );
}

struct StubRpc {
    url: String,
    methods: Arc<Mutex<Vec<String>>>,
}

impl StubRpc {
    fn start(accounts: HashMap<String, (String, Vec<u8>)>) -> Self {
        let listener = TcpListener::bind("127.0.0.1:0").expect("bind stub rpc");
        let url = format!("http://{}", listener.local_addr().expect("stub addr"));
        let methods = Arc::new(Mutex::new(Vec::new()));
        let seen = Arc::clone(&methods);
        std::thread::spawn(move || {
            for stream in listener.incoming() {
                let Ok(mut stream) = stream else { continue };
                let _ = serve_one(&mut stream, &accounts, &seen);
            }
        });
        Self { url, methods }
    }

    fn has_seen(&self, method: &str) -> bool {
        self.methods
            .lock()
            .expect("methods lock")
            .iter()
            .any(|recorded_method| recorded_method == method)
    }
}

fn serve_one(
    stream: &mut TcpStream,
    accounts: &HashMap<String, (String, Vec<u8>)>,
    seen: &Mutex<Vec<String>>,
) -> std::io::Result<()> {
    let mut reader = BufReader::new(stream.try_clone()?);
    let mut content_length = 0usize;
    loop {
        let mut line = String::new();
        if reader.read_line(&mut line)? == 0 {
            return Ok(());
        }
        let line = line.trim_end().to_ascii_lowercase();
        if line.is_empty() {
            break;
        }
        if let Some(length) = line.strip_prefix("content-length:") {
            content_length = length.trim().parse().unwrap_or(0);
        }
    }
    let mut body = vec![0u8; content_length];
    reader.read_exact(&mut body)?;
    let req: serde_json::Value = serde_json::from_slice(&body).unwrap_or_default();
    let method = req["method"].as_str().unwrap_or("").to_string();
    seen.lock().expect("methods lock").push(method.clone());
    let result = match method.as_str() {
        "getGenesisHash" => serde_json::json!("11111111111111111111111111111111"),
        "getAccountInfo" => {
            let address = req["params"][0].as_str().unwrap_or("");
            let value = accounts.get(address).map(|(owner, data)| {
                serde_json::json!({
                    "lamports": 1_000_000_000u64,
                    "owner": owner,
                    "data": [STANDARD.encode(data), "base64"],
                    "executable": true,
                    "rentEpoch": 0u64,
                    "space": data.len() as u64,
                })
            });
            serde_json::json!({ "context": { "slot": 1u64 }, "value": value })
        }
        _ => serde_json::Value::Null,
    };
    let response =
        serde_json::json!({ "jsonrpc": "2.0", "result": result, "id": req["id"] }).to_string();
    write!(
        stream,
        "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{response}",
        response.len(),
    )?;
    stream.flush()
}

fn write_keypair_json(path: &Path, keypair: &Keypair) {
    let bytes: Vec<u8> = keypair.to_bytes().to_vec();
    std::fs::write(path, serde_json::to_vec(&bytes).expect("serialize keypair"))
        .expect("write keypair file");
}

struct RunFixture {
    dataset_dir: tempfile::TempDir,
    keys_dir: tempfile::TempDir,
    rpc_stub: StubRpc,
    other_rpc_stub: StubRpc,
}

fn run_fixture() -> RunFixture {
    let dataset_dir = tempfile::tempdir().expect("dataset dir");
    let keys_dir = tempfile::tempdir().expect("keys dir");
    let program_keypair = Keypair::new();
    write_keypair_json(
        &keys_dir.path().join("distributor-program.json"),
        &program_keypair,
    );

    RunFixture {
        dataset_dir,
        keys_dir,
        rpc_stub: StubRpc::start(HashMap::new()),
        other_rpc_stub: StubRpc::start(HashMap::new()),
    }
}

fn write_valid_dataset(dir: &Path) -> PathBuf {
    let cosmos = distributor_test_support::CosmosKey::from_seed(1);
    let tree = distributor_test_support::synth_single_leaf_tree(&cosmos, 600, 0);
    let total_claimable = distributor_test_support::recount_total_claimable(&tree);
    let shard_leaf_counts = distributor_test_support::recount_shard_leaf_counts(&tree);
    let roots_commitment = distributor_test_support::roots_commitment_of(&tree);
    let leaf = tree.leaves.first().expect("single-leaf tree has one leaf");

    let entry = ClaimEntry {
        bech32_address: leaf.bech32_address.clone(),
        public_key: Some(hex::encode(leaf.compressed_public_key)),
        liquid_amount: leaf.liquid_amount,
        locked_amount: leaf.locked_amount,
        schedule: None,
        shard_id: leaf.shard_id,
        leaf_index: leaf.leaf_index,
        vesting_schedule_hash: hex::encode(leaf.vesting_schedule_hash),
        proof: leaf.proof.iter().map(hex::encode).collect(),
    };
    let shards = vec![ShardDataset {
        shard_id: leaf.shard_id,
        entries: vec![entry],
    }];
    let manifest_hash = distributor_dataset::manifest_hash_of(&shards);
    let ledger_content_hash = [0xab; 32];

    let attestation = Attestation {
        schema_version: distributor_dataset::ATTESTATION_SCHEMA_VERSION,
        roots_commitment: hex::encode(roots_commitment),
        shard_leaf_counts,
        total_claimable,
        leaf_count: 1,
        shard_count: distributor_core::consts::SHARD_COUNT,
        ledger_content_hash: hex::encode(ledger_content_hash),
        claim_dataset_manifest_hash: hex::encode(manifest_hash),
        git_commit: "test".into(),
        tool_version: "test".into(),
    };

    let claim_dir = dir.join("claim_dataset");
    std::fs::create_dir_all(&claim_dir).expect("create claim_dataset dir");
    let shard_json = serde_json::to_vec(&shards[0].entries).expect("serialize shard entries");
    let shard_path = claim_dir.join(format!("shard_{}.json", leaf.shard_id));
    std::fs::write(&shard_path, &shard_json).expect("write shard file");

    let manifest = serde_json::json!([{
        "shard_id": leaf.shard_id,
        "leaf_count": 1,
        "file_hash": hex::encode(distributor_core::hash::keccak256(&[&shard_json])),
        "file": format!("claim_dataset/shard_{}.json", leaf.shard_id),
    }]);
    std::fs::write(
        dir.join("manifest.json"),
        serde_json::to_vec_pretty(&manifest).expect("serialize manifest"),
    )
    .expect("write manifest.json");

    let roots_hex: Vec<String> = tree.roots.iter().map(hex::encode).collect();
    std::fs::write(
        dir.join("roots.json"),
        serde_json::to_vec_pretty(&roots_hex).expect("serialize roots"),
    )
    .expect("write roots.json");

    std::fs::write(
        dir.join("attestation.json"),
        serde_json::to_vec_pretty(&attestation).expect("serialize attestation"),
    )
    .expect("write attestation.json");

    let reconciliation = serde_json::json!({
        "supply_at_halt": total_claimable.to_string(),
        "total_claimable": total_claimable.to_string(),
        "treasury_total": "0",
        "ledger_content_hash": hex::encode(ledger_content_hash),
    });
    std::fs::write(
        dir.join("reconciliation.json"),
        serde_json::to_vec_pretty(&reconciliation).expect("serialize reconciliation"),
    )
    .expect("write reconciliation.json");

    shard_path
}

fn flip_one_byte(path: &Path) {
    let mut bytes = std::fs::read(path).expect("read shard file");
    let first = bytes.first_mut().expect("shard file is non-empty");
    *first ^= 0xff;
    std::fs::write(path, bytes).expect("write flipped shard file");
}

fn run_cmd(fixture: &RunFixture) -> Command {
    let mut cmd = Command::new(env!("CARGO_BIN_EXE_ceremony"));
    cmd.env_clear();
    cmd.arg("run")
        .arg("--dataset-dir")
        .arg(fixture.dataset_dir.path())
        .arg("--keys-dir")
        .arg(fixture.keys_dir.path())
        .arg("--rpc-url")
        .arg(&fixture.rpc_stub.url)
        .arg("--cluster")
        .arg("devnet")
        .arg("--other-rpc-url")
        .arg(&fixture.other_rpc_stub.url)
        .arg("--allow-unknown-genesis")
        .arg("--da-url")
        .arg("http://127.0.0.1:1")
        .arg("--skip-dvpn");
    cmd
}

#[test]
fn a_corrupted_dataset_halts_run_at_require_valid_dataset_before_any_transaction() {
    let fixture = run_fixture();
    let shard_file = write_valid_dataset(fixture.dataset_dir.path());
    flip_one_byte(&shard_file);

    let output = run_cmd(&fixture).output().expect("run ceremony");
    let stderr = String::from_utf8_lossy(&output.stderr);

    assert_eq!(
        output.status.code(),
        Some(ceremony::exit_code::USAGE),
        "a corrupted dataset must fail closed as USAGE; stderr: {stderr}"
    );
    assert!(
        stderr.contains("dataset self-verify failed"),
        "stderr must carry the require_valid_dataset gate's refusal: {stderr}"
    );
    for never in [
        "sendTransaction",
        "getMinimumBalanceForRentExemption",
        "getLatestBlockhash",
        "simulateTransaction",
    ] {
        assert!(
            !fixture.rpc_stub.has_seen(never),
            "{never} was sent — require_valid_dataset did not halt before tx work; methods seen: {:?}",
            fixture.rpc_stub.methods.lock().expect("methods lock")
        );
    }
}
