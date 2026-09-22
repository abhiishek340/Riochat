//! Portable (no-install, Windows) distribution: marker file, writable data
//! folder, per-copy instance id and bundled WebView2 runtime.

use std::io::Write;
use std::path::{Path, PathBuf};

pub const MARKER_FILE: &str = "portable";
pub const DATA_DIR: &str = "data";
pub const INSTANCE_ID_FILE: &str = "instance-id";
pub const WEBVIEW2_DIR: &str = "webview2";

/// Locations under the portable `data/` folder.
#[derive(Debug, Clone)]
pub struct Layout {
    pub data: PathBuf,
}

impl Layout {
    pub fn new(exe_dir: &Path) -> Self {
        Self {
            data: exe_dir.join(DATA_DIR),
        }
    }

    pub fn instance_id_file(&self) -> PathBuf {
        self.data.join(INSTANCE_ID_FILE)
    }

    pub fn tmp(&self) -> PathBuf {
        self.data.join("tmp")
    }

    pub fn webview(&self) -> PathBuf {
        self.data.join("webview")
    }
}

/// The marker only counts on Windows.
pub fn marker_present(exe_dir: &Path) -> bool {
    cfg!(windows) && exe_dir.join(MARKER_FILE).is_file()
}

/// A valid instance id is exactly 16 lowercase hex digits.
pub fn is_valid_instance_id(s: &str) -> bool {
    s.len() == 16 && s.bytes().all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
}

fn normalize_id(raw: &str) -> Option<String> {
    let t = raw.trim().to_ascii_lowercase();
    is_valid_instance_id(&t).then_some(t)
}

fn generate_id() -> String {
    use std::hash::{BuildHasher, Hasher};
    // RandomState is seeded from the operating system; the id only has to be
    // distinct between copies, it is not a secret.
    let mut h = std::collections::hash_map::RandomState::new().build_hasher();
    h.write_u32(std::process::id());
    h.write_u128(
        std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .map(|d| d.as_nanos())
            .unwrap_or(0),
    );
    format!("{:016x}", h.finish())
}

fn read_id(path: &Path) -> Option<String> {
    std::fs::read_to_string(path).ok().and_then(|s| normalize_id(&s))
}

/// Reads the instance id, creating it when missing or invalid. Creation uses
/// create-new so that when two starts race the loser adopts the winner's id.
pub fn instance_id(file: &Path) -> std::io::Result<String> {
    if let Some(id) = read_id(file) {
        return Ok(id);
    }
    if let Some(parent) = file.parent() {
        std::fs::create_dir_all(parent)?;
    }
    for _ in 0..4 {
        let candidate = generate_id();
        if file.exists() {
            // present but invalid (or being written by a racing start)
            for _ in 0..5 {
                std::thread::sleep(std::time::Duration::from_millis(20));
                if let Some(id) = read_id(file) {
                    return Ok(id);
                }
            }
            crate::fsutil::atomic_write(file, format!("{candidate}\n").as_bytes())?;
            return Ok(read_id(file).unwrap_or(candidate));
        }
        match std::fs::OpenOptions::new().write(true).create_new(true).open(file) {
            Ok(mut f) => {
                f.write_all(format!("{candidate}\n").as_bytes())?;
                f.sync_all()?;
                return Ok(candidate);
            }
            Err(e) if e.kind() == std::io::ErrorKind::AlreadyExists => continue,
            Err(e) => return Err(e),
        }
    }
    read_id(file).ok_or_else(|| std::io::Error::other("cannot create the instance id"))
}

/// Creates the data folder and proves it is writable with a real probe file.
pub fn prepare_data_folder(layout: &Layout) -> std::io::Result<()> {
    std::fs::create_dir_all(&layout.data)?;
    let probe = layout.data.join(format!(".write-probe-{}", std::process::id()));
    let result = (|| {
        let mut f = std::fs::File::create(&probe)?;
        f.write_all(b"probe")?;
        f.sync_all()
    })();
    let _ = std::fs::remove_file(&probe);
    result
}

/// Empties and recreates the sidecar's temp folder.
pub fn reset_tmp(layout: &Layout) -> std::io::Result<()> {
    let tmp = layout.tmp();
    if tmp.exists() {
        std::fs::remove_dir_all(&tmp)?;
    }
    std::fs::create_dir_all(&tmp)
}

/// Identifier used for the credential-store service and the single-instance
/// key of a portable copy.
pub fn portable_identifier(base: &str, instance_id: &str) -> String {
    format!("{base}.portable-{instance_id}")
}
