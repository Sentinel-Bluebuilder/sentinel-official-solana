use solana_keypair::Keypair;
use std::io;
use std::path::Path;

pub fn write_new_keypair_file(path: &Path, keypair: &Keypair) -> io::Result<()> {
    use std::io::Write;
    use zeroize::Zeroizing;
    let mut open_options = std::fs::OpenOptions::new();
    open_options.write(true).create_new(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        open_options.mode(0o600);
    }
    let mut file = open_options.open(path)?;

    let secret_bytes: Zeroizing<Vec<u8>> = Zeroizing::new(keypair.to_bytes().to_vec());
    let json: Zeroizing<Vec<u8>> = Zeroizing::new(serde_json::to_vec(&*secret_bytes)?);
    file.write_all(&json)?;

    file.sync_all()?;
    Ok(())
}
