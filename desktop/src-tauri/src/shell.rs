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

impl Shell {
    pub fn new(app: AppHandle, cfg: AppConfig, creds: Arc<dyn SecretStore>, proxy: proxy::State) -> Self {
        let webview_args = proxy::webview_args(&proxy.effective);
        Shell(Arc::new(Inner {
            app,
            cfg,
            model: Mutex::new(Model::new()),
            last_saved: Mutex::new(None),
            last_menu_count: Mutex::new(None),
            creds,
            sidecar: Sidecar::new(),
            proxy,
            oauth: Loopback::default(),
            downloads: Arc::new(Pending::default()),
            sign_in: tokio::sync::Mutex::new(()),
            webview_args,
        }))
    }

    pub fn app(&self) -> &AppHandle {
        &self.0.app
    }

    pub fn config(&self) -> &AppConfig {
        &self.0.cfg
    }

    pub fn policy(&self) -> &Policy {
        &self.0.cfg.policy
    }

    pub fn sidecar(&self) -> &Arc<Sidecar> {
        &self.0.sidecar
    }

    pub fn proxy_route(&self) -> proxy::Route {
        self.0.proxy.route()
    }

    fn model(&self) -> MutexGuard<'_, Model> {
        // a poisoned lock only means a panic elsewhere; the data is still valid
        self.0.model.lock().unwrap_or_else(|e| e.into_inner())
    }

    pub fn tab_count(&self) -> usize {
        self.model().len()
    }

    fn tabs_file(&self) -> PathBuf {
        self.0.cfg.config_dir.join("tabs.json")
    }

    fn network_file(&self) -> PathBuf {
        self.0.cfg.config_dir.join("network.json")
    }

    // ------------------------------------------------------------------
    // Startup
    // ------------------------------------------------------------------

    /// Creates the window and strip, restores saved tabs and starts the
    /// discard sweep. Runs in `setup`, after the single-instance hand-off.
    pub fn start(&self) -> Result<(), String> {
        let window = window::create_main(&self.0.app).map_err(|e| e.to_string())?;
        self.create_strip(&window).map_err(|e| e.to_string())?;

        let saved = Persisted::load(&self.tabs_file());
        let restored = tabs::sanitize(&saved, self.policy());
        for dropped in &restored.forbidden {
            log::info!("dropping a saved tab that policy forbids");
            self.forget_credential(dropped);
        }
        {
            let mut m = self.model();
            *m = Model::from_servers(&restored.servers, restored.active);
        }
        self.sync(true)?;

        let sweeper = self.clone();
        tauri::async_runtime::spawn(async move {
            let mut ticker = tokio::time::interval(tabs::SWEEP_EVERY);
            loop {
                ticker.tick().await;
                sweeper.sweep();
            }
        });
        Ok(())
    }

    // ------------------------------------------------------------------
    // Webviews
    // ------------------------------------------------------------------

    fn main_window(&self) -> Result<Window, String> {
        self.0
            .app
            .get_window(window::MAIN_LABEL)
            .ok_or_else(|| "the main window is not available".to_string())
    }

    /// Settings shared by every webview.
    fn common(&self, builder: WebviewBuilder<tauri::Wry>) -> WebviewBuilder<tauri::Wry> {
        let debug = cfg!(debug_assertions);
        let app = self.0.app.clone();
        let downloads = self.0.downloads.clone();
        #[allow(unused_mut)]
        let mut builder = builder
            .initialization_script(DESKTOP_MARKER_SCRIPT)
            .on_navigation(move |url| {
                let ok = window::navigation_allowed(url, debug);
                if !ok {
                    log::warn!("blocked navigation to {}", url.origin().ascii_serialization());
                }
                ok
            })
            .on_new_window({
                let app = app.clone();
                move |url, _features| window::deny_new_window(&app, url)
            })
            .on_download(move |webview, event| handle_download(&app, &downloads, &webview, event));
        #[cfg(windows)]
        {
            if !self.0.webview_args.is_empty() {
                builder = builder.additional_browser_args(&self.0.webview_args);
            }
            if let Some(layout) = &self.0.cfg.portable {
                builder = builder.data_directory(layout.webview());
            }
        }
        builder
    }

    fn create_strip(&self, window: &Window) -> tauri::Result<Webview> {
        let ((x, y, w, h), _) = window::layout_for(window)?;
        let builder = self.common(WebviewBuilder::new(
            window::STRIP_LABEL,
            WebviewUrl::App(window::STRIP_ROUTE.into()),
        ));
        window.add_child(
            builder,
            tauri::LogicalPosition::new(x, y),
            tauri::LogicalSize::new(w, h),
        )
    }

    fn create_tab_webview(&self, window: &Window, id: &str) -> tauri::Result<Webview> {
        let (_, (x, y, w, h)) = window::layout_for(window)?;
        let shell = self.clone();
        let builder = self
            .common(WebviewBuilder::new(id, WebviewUrl::App(window::TAB_ROUTE.into())))
            .on_document_title_changed(move |webview, title| shell.title_changed(webview.label(), &title));
        window.add_child(
            builder,
            tauri::LogicalPosition::new(x, y),
            tauri::LogicalSize::new(w, h),
        )
    }

    /// Re-lays out the strip and every content webview.
    pub fn relayout(&self) {
        let Ok(window) = self.main_window() else { return };
        let Ok((strip, content)) = window::layout_for(&window) else {
            return;
        };
        if let Some(w) = self.0.app.get_webview(window::STRIP_LABEL) {
            let _ = w.set_bounds(window::to_rect(strip));
        }
        let ids: Vec<String> = self
            .model()
            .tabs()
            .iter()
            .filter(|t| t.loaded)
            .map(|t| t.id.clone())
            .collect();
        for id in ids {
            if let Some(w) = self.0.app.get_webview(&id) {
                let _ = w.set_bounds(window::to_rect(content));
            }
        }
    }

    /// Makes the webviews match the model: the active tab has a webview and is
    /// shown, every other content webview is hidden.
    fn sync(&self, focus: bool) -> Result<(), String> {
        let window = self.main_window()?;
        let (active, tabs) = {
            let m = self.model();
            (
                m.active().map(str::to_string),
                m.tabs().iter().map(|t| (t.id.clone(), t.loaded)).collect::<Vec<_>>(),
            )
        };
        let Some(active) = active else { return Ok(()) };
        let active_loaded = tabs.iter().any(|(id, loaded)| *id == active && *loaded);
        if !active_loaded {
            self.create_tab_webview(&window, &active).map_err(|e| e.to_string())?;
            self.model().set_loaded(&active, true, Instant::now());
        }
        for (id, loaded) in &tabs {
            if *id != active && !*loaded {
                continue;
            }
            if let Some(w) = self.0.app.get_webview(id) {
                if *id == active {
                    let _ = w.show();
                    if focus {
                        let _ = w.set_focus();
                    }
                } else {
                    let _ = w.hide();
                }
            }
        }
        self.persist_and_notify();
        Ok(())
    }

    /// Closes the webviews of tabs hidden for too long. The tab reloads on its
    /// next activation and signs in again from the credential store.
    pub fn sweep(&self) {
        let now = Instant::now();
        let stale = self.model().discard_candidates(now, tabs::DISCARD_AFTER);
        for id in stale {
            if let Some(w) = self.0.app.get_webview(&id) {
                let _ = w.close();
            }
            self.model().set_loaded(&id, false, now);
            log::info!("discarded the webview of {id}");
        }
    }

    fn title_changed(&self, label: &str, title: &str) {
        let changed = self.model().set_title(label, title);
        if changed {
            self.persist_and_notify();
        }
    }

    /// Persists tabs, tells the strip and (macOS) refreshes the File menu.
    fn persist_and_notify(&self) {
        let (snapshot, persisted, count) = {
            let m = self.model();
            (m.snapshot(), m.persisted(), m.len())
        };
        let changed = {
            let mut last = self.0.last_saved.lock().unwrap_or_else(|e| e.into_inner());
            if last.as_ref() == Some(&persisted) {
                false
            } else {
                *last = Some(persisted.clone());
                true
            }
        };
        if changed {
            if let Err(e) = persisted.save(&self.tabs_file()) {
                log::warn!("cannot save tabs.json: {e}");
            }
        }
        let _ = self.0.app.emit_to(window::STRIP_LABEL, EVENT_TABS_CHANGED, &snapshot);
        #[cfg(target_os = "macos")]
        {
            let mut last = self.0.last_menu_count.lock().unwrap_or_else(|e| e.into_inner());
            if *last != Some(count) {
                *last = Some(count);
                crate::menu::install(&self.0.app, count);
            }
        }
        #[cfg(not(target_os = "macos"))]
        let _ = count;
    }

    // ------------------------------------------------------------------
    // Tab commands
    // ------------------------------------------------------------------

    pub fn tabs_list(&self) -> Snapshot {
        self.model().snapshot()
    }

    pub fn tabs_open(&self) -> Result<TabView, String> {
        let id = self.model().add(None);
        self.model().activate(&id, Instant::now())?;
        self.sync(true)?;
        let view = self.model().snapshot().tabs.into_iter().find(|t| t.id == id);
        view.ok_or_else(|| format!("unknown tab {id}"))
    }

    pub fn tabs_activate(&self, id: &str, focus: bool) -> Result<(), String> {
        self.model().activate(id, Instant::now())?;
        self.sync(focus)
    }

    pub fn tabs_close(&self, id: &str) -> Result<(), String> {
        let closed = self.model().close(id)?;
        if let Some(w) = self.0.app.get_webview(id) {
            let _ = w.close();
        }
        if let Some(server) = &closed.server {
            self.forget_server(server);
        }
        self.sync(true)
    }

    pub fn tabs_move(&self, id: &str, index: usize) -> Result<(), String> {
        self.model().move_tab(id, index)?;
        self.persist_and_notify();
        Ok(())
    }

    /// Closes the active tab (menu action); with one tab, hides the window.
    pub fn close_active_tab(&self) -> Result<(), String> {
        let active = self.model().active().map(str::to_string);
        match active {
            Some(id) => self.tabs_close(&id),
            None => Ok(()),
        }
    }

    // ------------------------------------------------------------------
    // Server binding
    // ------------------------------------------------------------------

    fn forget_credential(&self, server: &Server) {
        if let Err(e) = self.0.creds.delete(&creds::refresh_account(server)) {
            log::warn!("cannot delete a stored session: {e}");
        }
    }

    /// Deletes the refresh token and stops the sidecar when no tab uses it.
    fn forget_server(&self, server: &Server) {
        self.forget_credential(server);
        if server.is_local() && !self.model().uses_local() {
            self.0.sidecar.stop();
        }
    }

    fn require_tab(label: &str) -> Result<(), ShellError> {
        if label.starts_with(tabs::WEBVIEW_PREFIX) {
            Ok(())
        } else {
            Err(ShellError::tabs(format!("unknown tab {label}")))
        }
    }

    fn bound_server(&self, label: &str) -> Result<Option<Server>, ShellError> {
        Self::require_tab(label)?;
        match self.model().get(label) {
            Some(t) => Ok(t.server.clone()),
            None => Err(ShellError::tabs(format!("unknown tab {label}"))),
        }
    }

    fn launch(&self) -> Launch {
        let mut env = proxy::child_env(&self.0.proxy.effective);
        if let Some(layout) = &self.0.cfg.portable {
            let tmp = layout.tmp().to_string_lossy().into_owned();
            for key in ["TMP", "TEMP", "TMPDIR"] {
                env.push((key.to_string(), tmp.clone()));
            }
            let _ = std::fs::create_dir_all(layout.tmp());
        }
        Launch {
            data_dir: self.0.cfg.data_dir.join("local"),
            env,
        }
    }

    /// Policy check, then the live origin (starting the sidecar if needed).
    async fn live_origin(&self, server: &Server) -> Result<String, ShellError> {
        self.policy().check_server(server)?;
        match server.mode {
            ServerMode::Remote => Ok(server.origin.clone()),
            ServerMode::Local => self.0.sidecar.ensure_running(&self.0.app, &self.launch()).await,
        }
    }

    pub async fn get_server(&self, label: &str) -> Result<Option<ServerInfo>, ShellError> {
        let Some(server) = self.bound_server(label)? else {
            return Ok(None);
        };
        let origin = self.live_origin(&server).await?;
        Ok(Some(ServerInfo {
            mode: server.mode,
            origin,
        }))
    }

    pub async fn set_remote_server(&self, label: &str, origin: &str) -> Result<ServerInfo, ShellError> {
        Self::require_tab(label)?;
        let origin = normalize_origin(origin).ok_or_else(|| {
            ShellError::invalid_origin("the server address must be an absolute http(s) URL without a path")
        })?;
        self.policy().check_remote(&origin)?;
        self.model()
            .bind(label, Server::remote(origin.clone()))
            .map_err(ShellError::tabs)?;
        self.persist_and_notify();
        Ok(ServerInfo {
            mode: ServerMode::Remote,
            origin,
        })
    }

    pub async fn set_local_server(&self, label: &str) -> Result<ServerInfo, ShellError> {
        Self::require_tab(label)?;
        self.policy().check_local()?;
        let was_bound = self.bound_server(label)?.is_some();
        self.model().bind(label, Server::local()).map_err(ShellError::tabs)?;
        self.persist_and_notify();
        match self.live_origin(&Server::local()).await {
            Ok(origin) => Ok(ServerInfo {
                mode: ServerMode::Local,
                origin,
            }),
            Err(e) => {
                if !was_bound {
                    // return the tab to the setup screen so the user can retry
                    let _ = self.model().unbind(label);
                    self.persist_and_notify();
                }
                Err(e)
            }
        }
    }

    pub async fn leave_server(&self, label: &str) -> Result<(), ShellError> {
        Self::require_tab(label)?;
        let server = self.model().unbind(label).map_err(ShellError::tabs)?;
        if let Some(server) = server {
            self.forget_server(&server);
        }
        self.persist_and_notify();
        Ok(())
    }

    // ------------------------------------------------------------------
    // Sessions
    // ------------------------------------------------------------------

    pub fn store_session(&self, label: &str, refresh_token: &str) -> Result<(), ShellError> {
        let server = self.bound_server(label)?.ok_or_else(ShellError::no_server)?;
        let account = creds::refresh_account(&server);
        let token = refresh_token.trim();
        if token.is_empty() {
            self.0.creds.delete(&account)
        } else {
            self.0.creds.set(&account, token)
        }
    }

    pub fn clear_session(&self, label: &str) -> Result<(), ShellError> {
        match self.bound_server(label)? {
            Some(server) => self.0.creds.delete(&creds::refresh_account(&server)),
            None => Ok(()),
        }
    }

    fn store_rotation(&self, account: &str, outcome: &session::Outcome) -> Result<(), ShellError> {
        match &outcome.rotated_refresh_token {
            Some(token) => self.0.creds.set(account, token),
            None => Ok(()),
        }
    }

    pub async fn refresh_session(&self, label: &str) -> Result<Credentials, ShellError> {
        let server = self.bound_server(label)?.ok_or_else(ShellError::no_server)?;
        let account = creds::refresh_account(&server);
        let token = self.0.creds.get(&account)?.ok_or_else(ShellError::no_session)?;
        let origin = self.live_origin(&server).await?;
        match session::refresh(&self.proxy_route(), &origin, &token).await {
            Ok(outcome) => {
                self.store_rotation(&account, &outcome)?;
                Ok(outcome.credentials)
            }
            Err(e) => {
                if session::is_rejection(&e) {
                    let _ = self.0.creds.delete(&account);
                }
                Err(e)
            }
        }
    }

    pub async fn local_sign_in(&self, label: &str) -> Result<Credentials, ShellError> {
        let server = self.bound_server(label)?.ok_or_else(ShellError::no_server)?;
        if !server.is_local() {
            return Err(ShellError::invalid_origin("local sign-in requires local mode"));
        }
        self.policy().check_local()?;
        // calls are serialized process-wide
        let _serial = self.0.sign_in.lock().await;
        let account = creds::refresh_account(&server);
        let route = self.proxy_route();

        if let Some(token) = self.0.creds.get(&account)? {
            let origin = self.live_origin(&server).await?;
            match session::refresh(&route, &origin, &token).await {
                Ok(outcome) => {
                    self.store_rotation(&account, &outcome)?;
                    return Ok(outcome.credentials);
                }
                Err(e) if session::is_rejection(&e) => {
                    let _ = self.0.creds.delete(&account);
                }
                Err(e) => return Err(e),
            }
        }

        let (origin, grant) = self.0.sidecar.take_grant(&self.0.app, &self.launch()).await?;
        let outcome = session::exchange_grant(&route, &origin, &grant).await?;
        match &outcome.rotated_refresh_token {
            Some(token) => self.0.creds.set(&account, token)?,
            None => {
                return Err(ShellError::new(
                    ErrorKind::Http,
                    "local exchange returned no refresh token",
                ))
            }
        }
        Ok(outcome.credentials)
    }

    // ------------------------------------------------------------------
    // OAuth loopback
    // ------------------------------------------------------------------

    pub fn start_oauth_loopback(&self) -> Result<String, String> {
        let app = self.0.app.clone();
        self.0
            .oauth
            .start(move |url| {
                window::show_main(&app);
                let _ = app.emit(EVENT_OAUTH_CALLBACK, url);
            })
            .map_err(|e| format!("cannot start the sign-in listener: {e}"))
    }

    pub fn stop_oauth_loopback(&self) {
        self.0.oauth.stop();
    }

    // ------------------------------------------------------------------
    // Distribution and network proxy
    // ------------------------------------------------------------------

    pub fn distribution(&self) -> Distribution {
        let c = &self.0.cfg;
        Distribution::new(c.kind, c.update_mode, c.releases_url.clone(), &c.policy)
    }

    pub fn network_status(&self) -> Result<proxy::Status, proxy_settings::ProxyError> {
        let saved = proxy_settings::load(&self.network_file());
        let password = if saved.username.trim().is_empty() {
            None
        } else {
            self.0
                .creds
                .get(creds::PROXY_PASSWORD_ACCOUNT)
                .map_err(|e| proxy_settings::ProxyError::storage(e.message))?
        };
        Ok(self.0.proxy.status(
            &saved,
            password.as_deref(),
            &proxy_os::read_environment(),
            &proxy_os::read_os(),
            self.policy().proxy_managed(),
        ))
    }

    pub fn set_network_proxy(
        &self,
        settings: proxy_settings::Settings,
        password: Option<String>,
    ) -> Result<proxy::Status, proxy_settings::ProxyError> {
        if self.policy().proxy_managed() {
            return Err(proxy_settings::ProxyError::policy());
        }
        let valid = proxy_settings::validate(&settings)?;
        let storage = |e: ShellError| proxy_settings::ProxyError::storage(e.message);
        if valid.username.is_empty() {
            self.0.creds.delete(creds::PROXY_PASSWORD_ACCOUNT).map_err(storage)?;
        } else {
            match password.as_deref() {
                None => {}
                Some("") => self.0.creds.delete(creds::PROXY_PASSWORD_ACCOUNT).map_err(storage)?,
                Some(p) => self.0.creds.set(creds::PROXY_PASSWORD_ACCOUNT, p).map_err(storage)?,
            }
        }
        proxy_settings::save(&self.network_file(), &valid)?;
        self.network_status()
    }
}
