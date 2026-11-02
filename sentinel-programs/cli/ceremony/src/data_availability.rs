use anyhow::Context;
use distributor_core::hash::keccak256;
use distributor_dataset::{Attestation, decode_hex_array};
use distributor_merkle::roots_commitment;

pub trait DaTransport {
    fn get(&self, url: &str) -> anyhow::Result<Vec<u8>>;
}

#[must_use]
pub fn artifact_url(base_url: &str, name: &str) -> String {
    format!("{}/{}", base_url.trim_end_matches('/'), name)
}

pub fn require_retrievable(
    transport: &dyn DaTransport,
    base_url: &str,
    expected_roots_commitment: &[u8; 32],
) -> anyhow::Result<()> {
    let attestation_bytes = transport.get(&artifact_url(base_url, "attestation.json"))?;
    let attestation: Attestation =
        serde_json::from_slice(&attestation_bytes).context("attestation.json")?;
    if decode_hex_array::<32>(&attestation.roots_commitment)? != *expected_roots_commitment {
        anyhow::bail!("published attestation roots_commitment != deployed commitment");
    }

    let roots_bytes = transport.get(&artifact_url(base_url, "roots.json"))?;
    let roots_hex: Vec<String> = serde_json::from_slice(&roots_bytes).context("roots.json")?;
    let roots: Vec<[u8; 32]> = roots_hex
        .iter()
        .map(|hex_text| decode_hex_array::<32>(hex_text))
        .collect::<Result<_, _>>()
        .context("roots.json")?;

    if attestation.shard_leaf_counts.len() != roots.len() {
        anyhow::bail!(
            "served attestation shard_leaf_counts has {} entries != {} served shard roots",
            attestation.shard_leaf_counts.len(),
            roots.len()
        );
    }
    if hex::encode(roots_commitment(
        &roots,
        &attestation.shard_leaf_counts,
        attestation.total_claimable,
    )) != attestation.roots_commitment
    {
        anyhow::bail!("served roots.json does not bind the attestation roots_commitment");
    }

    let manifest_bytes = transport.get(&artifact_url(base_url, "manifest.json"))?;
    let manifest: serde_json::Value =
        serde_json::from_slice(&manifest_bytes).context("manifest.json")?;
    let mut preimage: Vec<u8> = Vec::new();
    for item in manifest.as_array().context("manifest.json: not an array")? {
        let file = item["file"]
            .as_str()
            .context("manifest entry missing `file`")?;
        let shard_id = u16::try_from(
            item["shard_id"]
                .as_u64()
                .context("manifest entry missing `shard_id`")?,
        )
        .context("manifest `shard_id` out of range")?;
        let expected_hash = item["file_hash"]
            .as_str()
            .context("manifest entry missing `file_hash`")?;
        let bytes = transport.get(&artifact_url(base_url, file))?;
        let file_hash = keccak256(&[&bytes]);
        if hex::encode(file_hash) != expected_hash {
            anyhow::bail!("served shard {file} does not match manifest hash");
        }
        preimage.extend_from_slice(&shard_id.to_le_bytes());
        preimage.extend_from_slice(&file_hash);
    }
    if hex::encode(keccak256(&[&preimage])) != attestation.claim_dataset_manifest_hash {
        anyhow::bail!("served dataset does not bind attestation claim_dataset_manifest_hash");
    }
    Ok(())
}

pub struct UreqTransport {
    pub timeout_seconds: u64,
    agent: ureq::Agent,
}

const MAX_BODY_BYTES: u64 = 256 * 1024 * 1024;
const DEFAULT_TIMEOUT_SECONDS: u64 = 30;

impl Default for UreqTransport {
    fn default() -> Self {
        Self::new(DEFAULT_TIMEOUT_SECONDS)
    }
}

impl UreqTransport {
    #[must_use]
    pub fn new(timeout_seconds: u64) -> Self {
        let agent = ureq::Agent::new_with_config(
            ureq::Agent::config_builder()
                .timeout_global(Some(std::time::Duration::from_secs(timeout_seconds)))
                .build(),
        );
        Self {
            timeout_seconds,
            agent,
        }
    }
}

impl DaTransport for UreqTransport {
    fn get(&self, url: &str) -> anyhow::Result<Vec<u8>> {
        let resp = self
            .agent
            .get(url)
            .call()
            .with_context(|| format!("GET {url}"))?;
        resp.into_body()
            .into_with_config()
            .limit(MAX_BODY_BYTES)
            .read_to_vec()
            .with_context(|| format!("GET {url} body"))
    }
}

#[cfg(test)]
#[allow(clippy::unwrap_used, clippy::indexing_slicing)]
mod tests {
    use super::*;
    use std::cell::RefCell;
    use std::collections::HashMap;

    #[derive(Default)]
    struct FakeDaTransport {
        stored: RefCell<HashMap<String, Vec<u8>>>,
    }
    impl FakeDaTransport {
        fn name_of(url: &str) -> String {
            url.rsplit('/').next().unwrap_or(url).to_string()
        }
        fn insert(&self, name: &str, bytes: &[u8]) {
            self.stored.borrow_mut().insert(name.into(), bytes.to_vec());
        }
    }
    impl DaTransport for FakeDaTransport {
        fn get(&self, url: &str) -> anyhow::Result<Vec<u8>> {
            self.stored
                .borrow()
                .get(&Self::name_of(url))
                .cloned()
                .with_context(|| format!("404 {url}"))
        }
    }

    fn shard_root() -> [u8; 32] {
        [0x33; 32]
    }
    fn roots() -> Vec<[u8; 32]> {
        vec![shard_root()]
    }

    fn leaf_counts() -> Vec<u32> {
        vec![0]
    }
    fn roots_commitment_fixture() -> [u8; 32] {
        roots_commitment(&roots(), &leaf_counts(), 0)
    }
    fn roots_json() -> Vec<u8> {
        format!(r#"["{}"]"#, hex::encode(shard_root())).into_bytes()
    }
    fn shard_bytes() -> Vec<u8> {
        b"[]".to_vec()
    }
    fn file_hash() -> [u8; 32] {
        keccak256(&[&shard_bytes()])
    }
    fn claim_manifest_hash() -> [u8; 32] {
        let mut pre = Vec::new();
        pre.extend_from_slice(&0u16.to_le_bytes());
        pre.extend_from_slice(&file_hash());
        keccak256(&[&pre])
    }
    fn attestation_json() -> Vec<u8> {
        format!(
            r#"{{"schema_version":{schema_version},"roots_commitment":"{commitment}","shard_leaf_counts":[0],"total_claimable":"0","leaf_count":0,"shard_count":128,"ledger_content_hash":"{commitment}","claim_dataset_manifest_hash":"{manifest_hash}","git_commit":"deadbeef","tool_version":"test"}}"#,
            schema_version = distributor_dataset::ATTESTATION_SCHEMA_VERSION,
            commitment = hex::encode(roots_commitment_fixture()),
            manifest_hash = hex::encode(claim_manifest_hash())
        )
        .into_bytes()
    }
    fn manifest_json() -> Vec<u8> {
        format!(
            r#"[{{"file":"shard_0.json","shard_id":0,"leaf_count":0,"file_hash":"{file_hash_hex}"}}]"#,
            file_hash_hex = hex::encode(file_hash())
        )
        .into_bytes()
    }

    fn seed_complete(transport: &FakeDaTransport) {
        transport.insert("attestation.json", &attestation_json());
        transport.insert("roots.json", &roots_json());
        transport.insert("manifest.json", &manifest_json());
        transport.insert("shard_0.json", &shard_bytes());
    }

    #[test]
    fn require_retrievable_passes_when_all_present_and_commitment_matches() {
        let transport = FakeDaTransport::default();
        seed_complete(&transport);
        assert!(
            require_retrievable(
                &transport,
                "https://da.example",
                &roots_commitment_fixture()
            )
            .is_ok()
        );
    }

    #[test]
    fn require_retrievable_fails_closed_on_missing_shard() {
        let transport = FakeDaTransport::default();
        seed_complete(&transport);
        transport.stored.borrow_mut().remove("shard_0.json");
        assert!(
            require_retrievable(
                &transport,
                "https://da.example",
                &roots_commitment_fixture()
            )
            .is_err()
        );
    }

    #[test]
    fn require_retrievable_fails_closed_on_commitment_mismatch() {
        let transport = FakeDaTransport::default();
        seed_complete(&transport);
        let other_commitment = [0x22; 32];
        assert!(require_retrievable(&transport, "https://da.example", &other_commitment).is_err());
    }

    #[test]
    fn require_retrievable_fails_closed_on_tampered_shard() {
        let transport = FakeDaTransport::default();
        seed_complete(&transport);
        transport.insert("shard_0.json", b"[{\"tampered\":true}]");
        assert!(
            require_retrievable(
                &transport,
                "https://da.example",
                &roots_commitment_fixture()
            )
            .is_err()
        );
    }

    #[test]
    fn require_retrievable_fails_closed_on_manifest_hash_mismatch() {
        let transport = FakeDaTransport::default();
        seed_complete(&transport);
        transport.insert("manifest.json", b"[]");
        assert!(
            require_retrievable(
                &transport,
                "https://da.example",
                &roots_commitment_fixture()
            )
            .is_err()
        );
    }

    #[test]
    fn ureq_transport_reads_bodies_larger_than_the_default_limit() {
        use std::io::{Read, Write};

        let transport = UreqTransport {
            timeout_seconds: DEFAULT_TIMEOUT_SECONDS,
            agent: ureq::Agent::new_with_config(
                ureq::Agent::config_builder()
                    .timeout_global(Some(std::time::Duration::from_secs(30)))
                    .proxy(None)
                    .build(),
            ),
        };

        const LEN: usize = 12 * 1024 * 1024;
        let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        let addr = listener.local_addr().unwrap();
        let server = std::thread::spawn(move || {
            let (mut stream, _) = listener.accept().unwrap();
            let mut req = Vec::new();
            let mut buf = [0u8; 4096];
            loop {
                let read_len = stream.read(&mut buf).unwrap();
                req.extend_from_slice(&buf[..read_len]);
                if read_len == 0 || req.windows(4).any(|window| window == b"\r\n\r\n") {
                    break;
                }
            }
            stream
                .write_all(
                    format!(
                        "HTTP/1.1 200 OK\r\nContent-Length: {LEN}\r\nConnection: close\r\n\r\n"
                    )
                    .as_bytes(),
                )
                .unwrap();
            stream.write_all(&vec![0x5a; LEN]).unwrap();
        });
        let body = transport
            .get(&format!("http://{addr}/shard_0.json"))
            .unwrap();
        assert_eq!(body.len(), LEN);
        server.join().unwrap();
    }
}
