//! The shell: ties the tab model to real webviews, owns the sidecar, the
//! credential store and the proxy decision, and implements every command.

use crate::creds::{self, SecretStore};
use crate::distribution::{Distribution, Kind, UpdateMode};
use crate::downloads::{self, Pending};
use crate::error::{ErrorKind, ShellError};
use crate::oauth::Loopback;
use crate::origin::normalize_origin;
use crate::policy::Policy;
use crate::portable::Layout;
use crate::proxy::{self, os as proxy_os, settings as proxy_settings};
use crate::server::{Server, ServerInfo, ServerMode};
use crate::session::{self, Credentials};
use crate::sidecar::{Launch, Sidecar};
use crate::tabs::{self, Model, Persisted, Snapshot, TabView};
use crate::window;
use std::path::PathBuf;
use std::sync::{Arc, Mutex, MutexGuard};
use std::time::Instant;
use tauri::webview::{DownloadEvent, WebviewBuilder};
use tauri::{AppHandle, Emitter, Manager, Webview, WebviewUrl, Window};

/// The web client treats this flag as "running inside the desktop shell".
pub const DESKTOP_MARKER_SCRIPT: &str = "window.__RIOCHAT_DESKTOP__ = true;";

pub const EVENT_TABS_CHANGED: &str = "tabs:changed";
pub const EVENT_OAUTH_CALLBACK: &str = "oauth-loopback-callback";
pub const EVENT_DOWNLOAD_FINISHED: &str = "downloads:finished";

/// Everything decided before the app starts.
pub struct AppConfig {
    pub identifier: String,
    pub kind: Kind,
    pub update_mode: UpdateMode,
    pub releases_url: String,
    pub policy: Policy,
    pub config_dir: PathBuf,
    pub data_dir: PathBuf,
    pub portable: Option<Layout>,
}

struct Inner {
    app: AppHandle,
    cfg: AppConfig,
    model: Mutex<Model>,
    last_saved: Mutex<Option<Persisted>>,
    #[cfg_attr(not(target_os = "macos"), allow(dead_code))]
    last_menu_count: Mutex<Option<usize>>,
    creds: Arc<dyn SecretStore>,
    sidecar: Arc<Sidecar>,
    proxy: proxy::State,
    oauth: Loopback,
    downloads: Arc<Pending>,
    sign_in: tokio::sync::Mutex<()>,
    #[cfg_attr(not(windows), allow(dead_code))]
    webview_args: String,
}

#[derive(Clone)]
pub struct Shell(Arc<Inner>);
