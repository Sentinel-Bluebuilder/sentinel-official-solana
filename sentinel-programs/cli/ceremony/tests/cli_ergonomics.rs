use base64::Engine as _;
use base64::engine::general_purpose::STANDARD;
use solana_sdk::signature::Keypair;
use std::collections::HashMap;
use std::io::{BufRead, BufReader, Read, Write};
use std::net::{TcpListener, TcpStream};
use std::path::Path;
use std::process::Command;

fn ceremony() -> Command {
    let mut command = Command::new(env!("CARGO_BIN_EXE_ceremony"));
    command.env_clear();
    command
}

#[test]
fn create_mint_and_init_distributor_are_wired_into_the_built_binary() {
    for sub in ["create-mint", "init-distributor"] {
        let output = ceremony()
            .args([sub, "--help"])
            .output()
            .unwrap_or_else(|error| panic!("run ceremony {sub} --help: {error}"));
        assert!(
            output.status.success(),
            "`ceremony {sub} --help` must succeed, got: {}",
            String::from_utf8_lossy(&output.stderr)
        );
        let stdout = String::from_utf8_lossy(&output.stdout);
        assert!(
            stdout.contains(sub),
            "`ceremony {sub} --help` should name the subcommand, got: {stdout}"
        );
    }
}

struct StubRpc {
    url: String,
}

impl StubRpc {
    fn start(accounts: HashMap<String, (String, Vec<u8>)>) -> Self {
        let listener = TcpListener::bind("127.0.0.1:0").expect("bind stub rpc");
        let url = format!("http://{}", listener.local_addr().expect("stub addr"));
        std::thread::spawn(move || {
            for stream in listener.incoming() {
                let Ok(mut stream) = stream else { continue };
                let _ = serve_one(&mut stream, &accounts);
            }
        });
        Self { url }
    }
}

fn serve_one(
    stream: &mut TcpStream,
    accounts: &HashMap<String, (String, Vec<u8>)>,
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

fn run_cmd(fixture: &RunFixture) -> Command {
    let mut cmd = ceremony();
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
fn run_dry_run_json_keeps_stdout_clean_and_sends_tracing_output_to_stderr() {
    let fixture = run_fixture();
    let output = run_cmd(&fixture)
        .args(["--json", "--dry-run"])
        .output()
        .expect("run ceremony");
    let stdout = String::from_utf8_lossy(&output.stdout);
    let stderr = String::from_utf8_lossy(&output.stderr);

    for line in stdout.lines().filter(|text| !text.is_empty()) {
        assert!(
            serde_json::from_str::<serde_json::Value>(line).is_ok(),
            "every non-empty stdout line must be a valid --json step record, got: {line}"
        );
    }
    assert!(
        !stdout.contains("INFO"),
        "a tracing log line leaked onto stdout, which must carry only --json records: {stdout:?}"
    );
    assert!(
        stderr.contains("INFO"),
        "structured tracing output must be observable on stderr: {stderr}"
    );
}
