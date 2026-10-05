//! The IPC surface. Every command is declared in `build.rs` and granted per
//! webview class in `capabilities/`: the strip may call the `tabs_*` commands,
//! content tabs the rest. Session commands derive the server from the calling
//! webview's label, never from a parameter.

use crate::distribution::Distribution;
use crate::error::ShellError;
use crate::proxy::settings::{ProxyError, Settings};
use crate::proxy::Status;
use crate::server::ServerInfo;
use crate::session::Credentials;
use crate::shell::Shell;
use crate::tabs::{Snapshot, TabView};
use crate::updates::{self, Found, Notice};
use serde::Serialize;
use tauri::{State, Webview};

#[derive(Debug, Serialize)]
pub struct OauthError {
    pub message: String,
}

// Commands that create or close webviews are `async` so they never run on the
// UI thread's synchronous command path (that deadlocks the web engine on
// Windows).

#[tauri::command]
pub async fn tabs_list(shell: State<'_, Shell>) -> Result<Snapshot, String> {
    Ok(shell.tabs_list())
}

#[tauri::command]
pub async fn tabs_open(shell: State<'_, Shell>) -> Result<TabView, String> {
    shell.tabs_open()
}

#[tauri::command]
pub async fn tabs_activate(shell: State<'_, Shell>, id: String, focus: Option<bool>) -> Result<(), String> {
    shell.tabs_activate(&id, focus.unwrap_or(true))
}

#[tauri::command]
pub async fn tabs_close(shell: State<'_, Shell>, id: String) -> Result<(), String> {
    shell.tabs_close(&id)
}

#[tauri::command]
pub async fn tabs_move(shell: State<'_, Shell>, id: String, index: usize) -> Result<(), String> {
    shell.tabs_move(&id, index)
}

#[tauri::command]
pub async fn get_distribution(shell: State<'_, Shell>) -> Result<Distribution, String> {
    Ok(shell.distribution())
}

#[tauri::command]
pub async fn check_update_notice(shell: State<'_, Shell>) -> Result<Option<Notice>, String> {
    updates::check_notice(&shell).await
}

#[tauri::command]
pub async fn check_update(shell: State<'_, Shell>, webview: Webview) -> Result<Option<Found>, String> {
    updates::check_install(&shell, &webview).await
}

#[tauri::command]
pub async fn get_network_proxy(shell: State<'_, Shell>) -> Result<Status, ProxyError> {
    shell.network_status()
}

#[tauri::command]
pub async fn set_network_proxy(
    shell: State<'_, Shell>,
    settings: Settings,
    password: Option<String>,
) -> Result<Status, ProxyError> {
    shell.set_network_proxy(settings, password)
}

#[tauri::command]
pub async fn get_server(shell: State<'_, Shell>, webview: Webview) -> Result<Option<ServerInfo>, ShellError> {
    shell.get_server(webview.label()).await
}

#[tauri::command]
pub async fn set_remote_server(
    shell: State<'_, Shell>,
    webview: Webview,
    origin: String,
) -> Result<ServerInfo, ShellError> {
    shell.set_remote_server(webview.label(), &origin).await
}

#[tauri::command]
pub async fn set_local_server(shell: State<'_, Shell>, webview: Webview) -> Result<ServerInfo, ShellError> {
    shell.set_local_server(webview.label()).await
}

#[tauri::command]
pub async fn leave_server(shell: State<'_, Shell>, webview: Webview) -> Result<(), ShellError> {
    shell.leave_server(webview.label()).await
}

#[tauri::command]
pub async fn local_sign_in(shell: State<'_, Shell>, webview: Webview) -> Result<Credentials, ShellError> {
    shell.local_sign_in(webview.label()).await
}

#[tauri::command]
pub async fn store_session(shell: State<'_, Shell>, webview: Webview, refresh_token: String) -> Result<(), ShellError> {
    shell.store_session(webview.label(), &refresh_token)
}

#[tauri::command]
pub async fn clear_session(shell: State<'_, Shell>, webview: Webview) -> Result<(), ShellError> {
    shell.clear_session(webview.label())
}

#[tauri::command]
pub async fn refresh_session(shell: State<'_, Shell>, webview: Webview) -> Result<Credentials, ShellError> {
    shell.refresh_session(webview.label()).await
}

#[tauri::command]
pub async fn start_oauth_loopback(shell: State<'_, Shell>) -> Result<String, OauthError> {
    shell.start_oauth_loopback().map_err(|message| OauthError { message })
}

#[tauri::command]
pub async fn stop_oauth_loopback(shell: State<'_, Shell>) -> Result<(), OauthError> {
    shell.stop_oauth_loopback();
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn oauth_error_shape() {
        let v = serde_json::to_value(OauthError { message: "x".into() }).unwrap();
        assert_eq!(v, serde_json::json!({ "message": "x" }));
    }
}
