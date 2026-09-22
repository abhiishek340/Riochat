//! Enterprise policy (Windows registry `SOFTWARE\Policies\Riochat`).
//!
//! Read once at startup. Per value the machine hive wins over the user hive;
//! the proxy values form one group that is never mixed across hives when the
//! machine hive sets a mode.

use crate::error::ShellError;
use crate::origin::normalize_origin;
use crate::proxy::address::{self, Endpoint};
use crate::proxy::bypass;
use crate::server::{Server, ServerMode};
use serde::Serialize;

pub const REGISTRY_PATH: &str = r"SOFTWARE\Policies\Riochat";

const MSG_LOCKED: &str = "server address is locked by your administrator";
const MSG_LOCAL_DISABLED: &str = "local mode is disabled by your administrator";
const MSG_UPDATES_DISABLED: &str = "update checks are disabled by your administrator";
const MSG_PROXY_MANAGED: &str = "the network proxy is managed by your administrator";

/// Values of one registry hive, already trimmed (empty strings are `None`).
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct RawHive {
    pub default_server_url: Option<String>,
    pub lock_server_url: Option<bool>,
    pub disable_local_mode: Option<bool>,
    pub disable_auto_update: Option<bool>,
    pub proxy_mode: Option<String>,
    pub proxy_server: Option<String>,
    pub proxy_bypass: Option<String>,
}

#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub enum ProxyPolicy {
    /// No proxy policy: the app setting decides.
    #[default]
    None,
    /// Use the OS / environment, ignoring the app setting.
    System,
    Direct,
    Fixed {
        endpoint: Endpoint,
        /// Normalized bypass list.
        bypass: String,
    },
}

#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct Policy {
    pub default_server_url: Option<String>,
    pub server_url_locked: bool,
    pub local_mode_disabled: bool,
    pub auto_update_disabled: bool,
    pub proxy: ProxyPolicy,
}

/// The five fields exposed to the web client.
#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct PolicyInfo {
    pub default_server_url: Option<String>,
    pub server_url_locked: bool,
    pub local_mode_allowed: bool,
    pub auto_update_disabled_by_policy: bool,
    pub proxy_managed_by_policy: bool,
}

fn pick<T: Clone>(machine: &Option<T>, user: &Option<T>) -> Option<T> {
    machine.clone().or_else(|| user.clone())
}

/// Parses `ProxyMode` plus its server and bypass strings. `None` means "no
/// usable proxy policy".
fn parse_proxy(mode: &str, server: Option<&str>, bypass_list: Option<&str>) -> ProxyPolicy {
    match mode.trim().to_ascii_lowercase().as_str() {
        "system" => ProxyPolicy::System,
        "direct" => ProxyPolicy::Direct,
        "fixed_servers" => match server.map(address::canonicalize) {
            Some(Ok(endpoint)) => ProxyPolicy::Fixed {
                endpoint,
                bypass: bypass::normalize(bypass_list.unwrap_or("")),
            },
            _ => {
                log::warn!("policy ProxyMode=fixed_servers without a usable ProxyServer is ignored");
                ProxyPolicy::None
            }
        },
        other => {
            log::warn!("policy ProxyMode {other:?} is not supported and is ignored");
            ProxyPolicy::None
        }
    }
}

impl Policy {
    /// Combines the two hives. `machine` wins per value.
    pub fn resolve(machine: &RawHive, user: &RawHive) -> Policy {
        let mut policy = Policy::default();

        if let Some(raw) = pick(&machine.default_server_url, &user.default_server_url) {
            match normalize_origin(&raw) {
                Some(origin) => policy.default_server_url = Some(origin),
                None => log::warn!("policy DefaultServerUrl is not a valid origin and is ignored"),
            }
        }
        let lock = pick(&machine.lock_server_url, &user.lock_server_url).unwrap_or(false);
        if lock {
            if policy.default_server_url.is_some() {
                policy.server_url_locked = true;
            } else {
                log::warn!("policy LockServerUrl is ignored without a valid DefaultServerUrl");
            }
        }
        policy.local_mode_disabled = pick(&machine.disable_local_mode, &user.disable_local_mode).unwrap_or(false);
        policy.auto_update_disabled = pick(&machine.disable_auto_update, &user.disable_auto_update).unwrap_or(false);

        policy.proxy = if let Some(mode) = &machine.proxy_mode {
            // the machine hive decides the whole group
            parse_proxy(mode, machine.proxy_server.as_deref(), machine.proxy_bypass.as_deref())
        } else if let Some(mode) = &user.proxy_mode {
            parse_proxy(
                mode,
                pick(&user.proxy_server, &machine.proxy_server).as_deref(),
                pick(&user.proxy_bypass, &machine.proxy_bypass).as_deref(),
            )
        } else {
            ProxyPolicy::None
        };
        policy
    }

    pub fn local_mode_allowed(&self) -> bool {
        !self.local_mode_disabled && !self.server_url_locked
    }

    pub fn proxy_managed(&self) -> bool {
        self.proxy != ProxyPolicy::None
    }

    pub fn info(&self) -> PolicyInfo {
        PolicyInfo {
            default_server_url: self.default_server_url.clone(),
            server_url_locked: self.server_url_locked,
            local_mode_allowed: self.local_mode_allowed(),
            auto_update_disabled_by_policy: self.auto_update_disabled,
            proxy_managed_by_policy: self.proxy_managed(),
        }
    }

    /// True when anything differs from the defaults.
    pub fn is_active(&self) -> bool {
        *self != Policy::default()
    }

    pub fn check_remote(&self, origin: &str) -> Result<(), ShellError> {
        if self.server_url_locked && self.default_server_url.as_deref() != Some(origin) {
            return Err(ShellError::policy(MSG_LOCKED));
        }
        Ok(())
    }

    pub fn check_local(&self) -> Result<(), ShellError> {
        if self.server_url_locked {
            return Err(ShellError::policy(MSG_LOCKED));
        }
        if self.local_mode_disabled {
            return Err(ShellError::policy(MSG_LOCAL_DISABLED));
        }
        Ok(())
    }

    pub fn check_server(&self, server: &Server) -> Result<(), ShellError> {
        match server.mode {
            ServerMode::Local => self.check_local(),
            ServerMode::Remote => self.check_remote(&server.origin),
        }
    }

    pub fn check_update(&self) -> Result<(), ShellError> {
        if self.auto_update_disabled {
            return Err(ShellError::policy(MSG_UPDATES_DISABLED));
        }
        Ok(())
    }

    pub fn proxy_edit_error(&self) -> Option<&'static str> {
        self.proxy_managed().then_some(MSG_PROXY_MANAGED)
    }
}

#[cfg(windows)]
fn read_hive(root: winreg::HKEY) -> RawHive {
    use winreg::RegKey;
    let Ok(key) = RegKey::predef(root).open_subkey(REGISTRY_PATH) else {
        return RawHive::default();
    };
    let text = |name: &str| -> Option<String> {
        key.get_value::<String, _>(name)
            .ok()
            .map(|s| s.trim().to_string())
            .filter(|s| !s.is_empty())
    };
    let flag = |name: &str| -> Option<bool> { key.get_value::<u32, _>(name).ok().map(|v| v != 0) };
    RawHive {
        default_server_url: text("DefaultServerUrl"),
        lock_server_url: flag("LockServerUrl"),
        disable_local_mode: flag("DisableLocalMode"),
        disable_auto_update: flag("DisableAutoUpdate"),
        proxy_mode: text("ProxyMode"),
        proxy_server: text("ProxyServer"),
        proxy_bypass: text("ProxyBypassList"),
    }
}
