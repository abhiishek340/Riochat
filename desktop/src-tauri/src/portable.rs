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

pub const WRITE_ERROR_MESSAGE: &str = "Riochat Portable is unable to save files into its data folder.\n\
Please relocate the Riochat folder to a location you can write to (avoid read-only drives and protected directories), then launch it again. \
Your data will never be saved in any other place.";

pub const WEBVIEW2_DOWNLOAD_URL: &str = "https://developer.microsoft.com/microsoft-edge/webview2/";

pub fn webview2_missing_message(bundled: bool) -> String {
    let en = if bundled {
        "The WebView2 runtime bundled in this portable copy failed to load. The offline package could be damaged or incomplete, so please download it once more."
    } else {
        "Riochat relies on the Microsoft Edge WebView2 runtime, and it could not be found on this computer."
    };
    format!("{en}\n\nOpen the download page?\n({WEBVIEW2_DOWNLOAD_URL})")
}

/// Security identifiers of the two AppContainer groups that need read and
/// execute rights on an extracted Fixed Version runtime: ALL APPLICATION
/// PACKAGES and ALL RESTRICTED APPLICATION PACKAGES.
pub const APPCONTAINER_SIDS: [&str; 2] = ["*S-1-15-2-1", "*S-1-15-2-2"];
const ACL_STAMP: &str = ".riochat-acl-granted";

/// Grants the AppContainer groups access to a Fixed Version runtime folder,
/// once per extracted folder (tracked by a stamp file inside it).
#[cfg_attr(not(windows), allow(dead_code))]
pub fn grant_runtime_access(runtime_dir: &Path) -> std::io::Result<()> {
    let stamp = runtime_dir.join(ACL_STAMP);
    if stamp.exists() {
        return Ok(());
    }
    for sid in APPCONTAINER_SIDS {
        let status = std::process::Command::new("icacls")
            .arg(runtime_dir)
            .arg("/grant")
            .arg(format!("{sid}:(OI)(CI)(RX)"))
            .args(["/T", "/C", "/Q"])
            .status()?;
        if !status.success() {
            return Err(std::io::Error::other(format!("icacls failed with {status}")));
        }
    }
    std::fs::write(&stamp, b"granted\n")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn id_validity() {
        assert!(is_valid_instance_id("0123456789abcdef"));
        for bad in [
            "",
            "0123456789ABCDEF",
            "0123456789abcde",
            "0123456789abcdef0",
            "../etc/passwd....",
            "0123456789abcdeg",
        ] {
            assert!(!is_valid_instance_id(bad), "{bad}");
        }
    }

    #[test]
    fn repeated_creation_is_stable() {
        let d = tempfile::tempdir().unwrap();
        let f = d.path().join("data").join("instance-id");
        let a = instance_id(&f).unwrap();
        let b = instance_id(&f).unwrap();
        assert_eq!(a, b);
        assert!(is_valid_instance_id(&a));
    }

    #[test]
    fn copied_file_keeps_id_and_separate_folders_differ() {
        let d = tempfile::tempdir().unwrap();
        let f1 = d.path().join("a").join("instance-id");
        let f2 = d.path().join("b").join("instance-id");
        let id1 = instance_id(&f1).unwrap();
        std::fs::create_dir_all(f2.parent().unwrap()).unwrap();
        std::fs::copy(&f1, &f2).unwrap();
        assert_eq!(instance_id(&f2).unwrap(), id1);
        let f3 = d.path().join("c").join("instance-id");
        assert_ne!(instance_id(&f3).unwrap(), id1);
    }

    #[test]
    fn stored_ids_are_trimmed_and_lowercased_invalid_replaced() {
        let d = tempfile::tempdir().unwrap();
        let f = d.path().join("instance-id");
        std::fs::write(&f, "  0123456789ABCDEF \r\n").unwrap();
        assert_eq!(instance_id(&f).unwrap(), "0123456789abcdef");
        for bad in ["", "garbage", "../../x", "0123", "zzzzzzzzzzzzzzzz"] {
            std::fs::write(&f, bad).unwrap();
            let id = instance_id(&f).unwrap();
            assert!(is_valid_instance_id(&id), "{bad:?} -> {id}");
            assert_eq!(std::fs::read_to_string(&f).unwrap().trim(), id);
        }
    }

    #[test]
    fn prepare_leaves_no_probe_and_fails_when_unwritable() {
        let d = tempfile::tempdir().unwrap();
        let layout = Layout::new(d.path());
        prepare_data_folder(&layout).unwrap();
        let leftovers: Vec<_> = std::fs::read_dir(&layout.data).unwrap().collect();
        assert!(leftovers.is_empty());

        // a regular file where the folder should be cannot be written into
        let blocked = tempfile::tempdir().unwrap();
        std::fs::write(blocked.path().join(DATA_DIR), b"x").unwrap();
        let err = prepare_data_folder(&Layout::new(blocked.path())).unwrap_err();
        assert!(!err.to_string().is_empty());
    }

    #[test]
    fn tmp_is_emptied() {
        let d = tempfile::tempdir().unwrap();
        let layout = Layout::new(d.path());
        std::fs::create_dir_all(layout.tmp()).unwrap();
        std::fs::write(layout.tmp().join("old"), b"1").unwrap();
        reset_tmp(&layout).unwrap();
        assert!(layout.tmp().is_dir());
        assert_eq!(std::fs::read_dir(layout.tmp()).unwrap().count(), 0);
    }

    #[test]
    fn identity_and_messages() {
        assert_eq!(
            portable_identifier("com.riochat.desktop", "0123456789abcdef"),
            "com.riochat.desktop.portable-0123456789abcdef"
        );
        assert!(webview2_missing_message(true).contains("bundled"));
        assert!(webview2_missing_message(false).contains(WEBVIEW2_DOWNLOAD_URL));
        assert!(WRITE_ERROR_MESSAGE.contains("Riochat Portable"));
    }
}
