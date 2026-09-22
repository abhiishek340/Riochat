//! Pre-start work: distribution kind, portable preparation, WebView2 checks
//! and policy. Fatal problems show a native dialog and exit with status 1.

use crate::distribution::{self, Bundle, Kind, UpdateMode};
use crate::fsutil::strip_verbatim;
use crate::policy::{self, Policy};
use crate::portable::{self, Layout};
use std::path::PathBuf;

pub struct Boot {
    /// Identifier used for the credential-store service and single-instance
    /// key (the base identifier, plus `.portable-<id>` for portable copies).
    pub identifier: String,
    pub kind: Kind,
    pub portable: Option<Layout>,
    pub policy: Policy,
    pub update_mode: UpdateMode,
    pub releases_url: String,
}

fn bundle_info() -> Bundle {
    use tauri::utils::config::BundleType;
    match tauri::utils::platform::bundle_type() {
        None => Bundle::Unknown,
        Some(BundleType::Msi) => Bundle::Msi,
        Some(_) => Bundle::Other,
    }
}

fn exe_dir() -> Option<PathBuf> {
    let exe = std::env::current_exe().ok()?;
    exe.parent().map(strip_verbatim)
}

pub fn prepare(base_identifier: &str, first_endpoint: Option<&str>, version: &str) -> Boot {
    let dir = exe_dir();
    let marker = dir.as_deref().map(portable::marker_present).unwrap_or(false);
    let kind = distribution::classify(marker, cfg!(debug_assertions), bundle_info());

    let mut identifier = base_identifier.to_string();
    let mut layout = None;
    if kind == Kind::Portable {
        let dir = dir.clone().unwrap_or_else(|| PathBuf::from("."));
        let l = Layout::new(&dir);
        if let Err(e) = portable::prepare_data_folder(&l) {
            log::error!("portable data folder is not writable: {e}");
            native::fatal(portable::WRITE_ERROR_MESSAGE);
            std::process::exit(1);
        }
        match portable::instance_id(&l.instance_id_file()) {
            Ok(id) => identifier = portable::portable_identifier(base_identifier, &id),
            Err(e) => {
                log::error!("cannot create the portable instance id: {e}");
                native::fatal(portable::WRITE_ERROR_MESSAGE);
                std::process::exit(1);
            }
        }
        layout = Some(l);
    }

    #[cfg(windows)]
    ensure_webview2(dir.as_deref());

    let policy = policy::load();
    let update_mode = distribution::update_mode(kind, policy.auto_update_disabled);
    let releases_url = distribution::releases_url(first_endpoint, version);
    Boot {
        identifier,
        kind,
        portable: layout,
        policy,
        update_mode,
        releases_url,
    }
}

/// Uses a bundled Fixed Version runtime when present, then verifies that a
/// WebView2 runtime can be loaded at all.
#[cfg(windows)]
fn ensure_webview2(exe_dir: Option<&std::path::Path>) {
    let bundled = exe_dir.map(|d| d.join(portable::WEBVIEW2_DIR)).filter(|p| p.is_dir());
    if let Some(runtime) = &bundled {
        // used even if incomplete, so a broken offline package fails loudly
        std::env::set_var("WEBVIEW2_BROWSER_EXECUTABLE_FOLDER", runtime);
        if let Err(e) = portable::grant_runtime_access(runtime) {
            log::warn!("cannot grant the WebView2 runtime access rights: {e}");
        }
    }
    if tauri::webview_version().is_err() {
        if native::confirm(&portable::webview2_missing_message(bundled.is_some())) {
            native::open_url(portable::WEBVIEW2_DOWNLOAD_URL);
        }
        std::process::exit(1);
    }
}

/// Native message boxes (Windows) used before any window exists.
pub mod native {
    #[cfg(windows)]
    mod imp {
        use windows_sys::Win32::UI::WindowsAndMessaging::{
            MessageBoxW, IDYES, MB_ICONERROR, MB_OK, MB_YESNO, MESSAGEBOX_STYLE,
        };

        fn wide(s: &str) -> Vec<u16> {
            s.encode_utf16().chain(std::iter::once(0)).collect()
        }

        fn show(message: &str, style: MESSAGEBOX_STYLE) -> i32 {
            let text = wide(message);
            let title = wide("Riochat");
            // SAFETY: both buffers are NUL-terminated and outlive the call.
            unsafe { MessageBoxW(std::ptr::null_mut(), text.as_ptr(), title.as_ptr(), style) }
        }

        pub fn fatal(message: &str) {
            show(message, MB_OK | MB_ICONERROR);
        }

        pub fn confirm(message: &str) -> bool {
            show(message, MB_YESNO | MB_ICONERROR) == IDYES
        }

        pub fn open_url(url: &str) {
            let _ = std::process::Command::new("rundll32")
                .args(["url.dll,FileProtocolHandler", url])
                .spawn();
        }
    }

    #[cfg(not(windows))]
    mod imp {
        pub fn fatal(_message: &str) {}

        pub fn confirm(_message: &str) -> bool {
            false
        }

        pub fn open_url(_url: &str) {}
    }

    /// An error dialog with an OK button (Windows); other platforms log only.
    pub fn fatal(message: &str) {
        eprintln!("{message}");
        imp::fatal(message);
    }

    /// An error dialog with Yes/No buttons; true for Yes.
    #[cfg_attr(not(windows), allow(dead_code))]
    pub fn confirm(message: &str) -> bool {
        eprintln!("{message}");
        imp::confirm(message)
    }

    /// Opens a web page in the default browser.
    #[cfg_attr(not(windows), allow(dead_code))]
    pub fn open_url(url: &str) {
        imp::open_url(url);
    }
}
