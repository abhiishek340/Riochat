//! Update checks. The shell checks through the updater plugin using the
//! shell's own proxy route; installation is only ever possible in `auto` mode.

use crate::distribution::{disabled_message, UpdateMode};
use crate::shell::Shell;
use serde::Serialize;
use std::time::Duration;
use tauri::{Manager, Webview};
use tauri_plugin_updater::UpdaterExt;

pub const CHECK_TIMEOUT: Duration = Duration::from_secs(20);

/// Granted to content tabs at runtime, and only in auto-update mode, so
/// portable and MSI builds can never install an update.
pub const RUNTIME_CAPABILITY: &str = r#"{
  "identifier": "updater-runtime",
  "description": "Update checks and installation for content tabs (auto-update mode only).",
  "webviews": ["tab-*"],
  "permissions": ["updater:default", "allow-check-update"]
}"#;

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Notice {
    pub version: String,
    pub current_version: String,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Found {
    pub rid: u32,
    pub current_version: String,
    pub version: String,
    pub raw_json: serde_json::Value,
}

/// The first updater endpoint from the application configuration.
pub fn first_endpoint(config: &tauri::Config) -> Option<String> {
    config
        .plugins
        .0
        .get("updater")?
        .get("endpoints")?
        .as_array()?
        .first()?
        .as_str()
        .map(str::to_string)
}

fn builder(shell: &Shell) -> tauri_plugin_updater::UpdaterBuilder {
    let app = shell.app();
    let mut b = app.updater_builder().timeout(CHECK_TIMEOUT);
    let proxy = first_endpoint(app.config())
        .and_then(|e| url::Url::parse(&e).ok())
        .and_then(|u| shell.proxy_route().proxy_for(&u))
        .and_then(|e| e.to_url());
    b = match proxy {
        Some(p) => b.proxy(p),
        None => b.no_proxy(),
    };
    b
}

async fn check(shell: &Shell) -> Result<Option<tauri_plugin_updater::Update>, String> {
    builder(shell)
        .build()
        .map_err(|e| e.to_string())?
        .check()
        .await
        .map_err(|e| e.to_string())
}

/// Check only; downloads nothing. Allowed in `auto` and `notify` modes.
pub async fn check_notice(shell: &Shell) -> Result<Option<Notice>, String> {
    let mode = shell.config().update_mode;
    if mode == UpdateMode::Disabled {
        return Err(disabled_message(shell.policy()).message);
    }
    Ok(check(shell).await?.map(|u| Notice {
        version: u.version,
        current_version: u.current_version,
    }))
}

/// Check and keep the update in the calling webview's resource table so the
/// updater plugin can download and install it by `rid`. `auto` mode only.
pub async fn check_install(shell: &Shell, webview: &Webview) -> Result<Option<Found>, String> {
    if shell.config().update_mode != UpdateMode::Auto {
        return Err(disabled_message(shell.policy()).message);
    }
    Ok(check(shell).await?.map(|update| {
        let current_version = update.current_version.clone();
        let version = update.version.clone();
        let raw_json = update.raw_json.clone();
        let rid = webview.resources_table().add(update);
        Found {
            rid,
            current_version,
            version,
            raw_json,
        }
    }))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn runtime_capability_is_valid() {
        let cap: tauri::utils::acl::capability::Capability =
            serde_json::from_str(RUNTIME_CAPABILITY).expect("capability JSON must parse");
        assert_eq!(cap.identifier, "updater-runtime");
        assert_eq!(cap.webviews, vec!["tab-*".to_string()]);
        assert!(cap.windows.is_empty());
    }

    #[test]
    fn endpoint_from_config() {
        let json = r#"{"plugins":{"updater":{"endpoints":["https://github.com/a/b/releases/latest/download/latest.json","https://x"]}}}"#;
        let v: serde_json::Value = serde_json::from_str(json).unwrap();
        let first = v["plugins"]["updater"]["endpoints"][0].as_str().unwrap();
        assert!(first.starts_with("https://github.com/a/b"));
    }
}
