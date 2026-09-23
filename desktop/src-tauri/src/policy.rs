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

/// Loads the effective policy. Other platforms always use the defaults.
pub fn load() -> Policy {
    #[cfg(windows)]
    {
        use winreg::enums::{HKEY_CURRENT_USER, HKEY_LOCAL_MACHINE};
        let policy = Policy::resolve(&read_hive(HKEY_LOCAL_MACHINE), &read_hive(HKEY_CURRENT_USER));
        if policy.is_active() {
            log::info!("enterprise policy is active: {:?}", policy.info());
        }
        policy
    }
    #[cfg(not(windows))]
    {
        Policy::default()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn hive() -> RawHive {
        RawHive::default()
    }

    #[test]
    fn default_policy_info_has_exactly_five_fields() {
        let v = serde_json::to_value(Policy::default().info()).unwrap();
        let obj = v.as_object().unwrap();
        assert_eq!(obj.len(), 5);
        assert!(v["defaultServerUrl"].is_null());
        assert_eq!(v["serverUrlLocked"], false);
        assert_eq!(v["localModeAllowed"], true);
        assert_eq!(v["autoUpdateDisabledByPolicy"], false);
        assert_eq!(v["proxyManagedByPolicy"], false);
    }

    #[test]
    fn locked_default_is_normalized_and_enforced() {
        let user = RawHive {
            default_server_url: Some("  HTTPS://Chat.Corp:443/ ".into()),
            lock_server_url: Some(true),
            ..hive()
        };
        let p = Policy::resolve(&hive(), &user);
        assert_eq!(p.default_server_url.as_deref(), Some("https://chat.corp"));
        assert!(p.server_url_locked);
        assert!(!p.local_mode_allowed());
        assert!(p.check_remote("https://chat.corp").is_ok());
        for other in ["https://other.corp", "http://chat.corp", "https://chat.corp:8443"] {
            let e = p.check_remote(other).unwrap_err();
            assert_eq!(e.message, "policy: server address is locked by your administrator");
        }
        assert_eq!(
            p.check_local().unwrap_err().message,
            "policy: server address is locked by your administrator"
        );
    }

    #[test]
    fn lock_without_valid_default_is_ignored() {
        let p = Policy::resolve(
            &hive(),
            &RawHive {
                lock_server_url: Some(true),
                default_server_url: Some("not a url".into()),
                ..hive()
            },
        );
        assert!(!p.server_url_locked);
        assert!(p.check_remote("https://anything.example").is_ok());
        assert!(p.local_mode_allowed());
    }

    #[test]
    fn default_without_lock_restricts_nothing() {
        let p = Policy::resolve(
            &hive(),
            &RawHive {
                default_server_url: Some("https://a.b".into()),
                ..hive()
            },
        );
        assert_eq!(p.default_server_url.as_deref(), Some("https://a.b"));
        assert!(p.check_remote("https://x.y").is_ok());
        assert!(p.check_local().is_ok());
    }

    #[test]
    fn disabling_local_mode_alone() {
        let p = Policy::resolve(
            &RawHive {
                disable_local_mode: Some(true),
                ..hive()
            },
            &hive(),
        );
        assert_eq!(
            p.check_local().unwrap_err().message,
            "policy: local mode is disabled by your administrator"
        );
        assert!(p.check_remote("https://x.y").is_ok());
        assert!(!p.info().local_mode_allowed);
    }

    #[test]
    fn machine_hive_beats_user_hive_even_for_false() {
        let machine = RawHive {
            disable_auto_update: Some(false),
            disable_local_mode: Some(false),
            ..hive()
        };
        let user = RawHive {
            disable_auto_update: Some(true),
            disable_local_mode: Some(true),
            ..hive()
        };
        let p = Policy::resolve(&machine, &user);
        assert!(!p.auto_update_disabled);
        assert!(!p.local_mode_disabled);
        let p = Policy::resolve(&hive(), &user);
        assert!(p.auto_update_disabled);
        assert_eq!(
            p.check_update().unwrap_err().message,
            "policy: update checks are disabled by your administrator"
        );
    }

    #[test]
    fn machine_proxy_mode_decides_the_whole_group() {
        let machine = RawHive {
            proxy_mode: Some("Direct".into()),
            ..hive()
        };
        let user = RawHive {
            proxy_mode: Some("fixed_servers".into()),
            proxy_server: Some("p:1".into()),
            ..hive()
        };
        assert_eq!(Policy::resolve(&machine, &user).proxy, ProxyPolicy::Direct);
        // unusable machine mode is not replaced by the user's
        let bad_machine = RawHive {
            proxy_mode: Some("pac_script".into()),
            ..hive()
        };
        assert_eq!(Policy::resolve(&bad_machine, &user).proxy, ProxyPolicy::None);
    }

    #[test]
    fn user_proxy_falls_back_to_machine_server_and_bypass() {
        let machine = RawHive {
            proxy_server: Some("proxy.corp:3128".into()),
            proxy_bypass: Some("10.*;.corp".into()),
            ..hive()
        };
        let user = RawHive {
            proxy_mode: Some("FIXED_SERVERS".into()),
            ..hive()
        };
        match Policy::resolve(&machine, &user).proxy {
            ProxyPolicy::Fixed { endpoint, bypass } => {
                assert_eq!(endpoint.address(), "http://proxy.corp:3128");
                assert!(bypass.starts_with("10.0.0.0/8,.corp"));
            }
            other => panic!("unexpected {other:?}"),
        }
    }

    #[test]
    fn unusable_fixed_server_means_no_policy() {
        for server in [None, Some("https://p:1"), Some("u:p@x:1"), Some("")] {
            let user = RawHive {
                proxy_mode: Some("fixed_servers".into()),
                proxy_server: server.map(|s| s.to_string()),
                ..hive()
            };
            assert_eq!(Policy::resolve(&hive(), &user).proxy, ProxyPolicy::None, "{server:?}");
        }
        let p = Policy::resolve(
            &hive(),
            &RawHive {
                proxy_mode: Some("system".into()),
                ..hive()
            },
        );
        assert_eq!(p.proxy, ProxyPolicy::System);
        assert!(p.proxy_managed());
        assert!(p.proxy_edit_error().is_some());
    }
}
