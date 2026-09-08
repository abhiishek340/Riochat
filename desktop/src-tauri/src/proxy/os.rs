//! Proxy settings read from the process environment and the operating system.

use super::address::{canonicalize, canonicalize_with_credentials, Endpoint};
use super::bypass;

/// A static proxy configuration found in the environment or the OS.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Detected {
    pub http: Option<Endpoint>,
    pub https: Option<Endpoint>,
    /// Normalized bypass list (empty when none was configured).
    pub bypass: String,
    /// A PAC script is configured. It is never applied.
    pub pac: bool,
}

impl Detected {
    pub fn has_proxy(&self) -> bool {
        self.http.is_some() || self.https.is_some()
    }
}

fn first_set<F: Fn(&str) -> Option<String>>(lookup: &F, names: &[&str]) -> Option<String> {
    names
        .iter()
        .filter_map(|n| lookup(n))
        .map(|v| v.trim().to_string())
        .find(|v| !v.is_empty())
}

/// Reads `HTTP_PROXY`, `HTTPS_PROXY`, `ALL_PROXY` and `NO_PROXY` (either case)
/// through `lookup`. `ALL_PROXY` fills a scheme that has no proxy of its own.
pub fn from_environment<F: Fn(&str) -> Option<String>>(lookup: F) -> Detected {
    let parse = |names: &[&str]| -> Option<Endpoint> {
        let raw = first_set(&lookup, names)?;
        match canonicalize_with_credentials(&raw) {
            Ok(e) => Some(e),
            Err(err) => {
                log::warn!("ignoring unusable proxy variable ({}): {}", names[0], err);
                None
            }
        }
    };
    let all = parse(&["ALL_PROXY", "all_proxy"]);
    let http = parse(&["HTTP_PROXY", "http_proxy"]).or_else(|| all.clone());
    let https = parse(&["HTTPS_PROXY", "https_proxy"]).or_else(|| all.clone());
    let no_proxy = first_set(&lookup, &["NO_PROXY", "no_proxy"]).unwrap_or_default();
    let have = http.is_some() || https.is_some();
    Detected {
        http,
        https,
        bypass: if have {
            bypass::normalize(&no_proxy)
        } else {
            String::new()
        },
        pac: false,
    }
}

pub fn read_environment() -> Detected {
    from_environment(|name| std::env::var(name).ok())
}

fn strip_scheme_prefix(value: &str) -> &str {
    value.split_once("://").map(|(_, rest)| rest).unwrap_or(value)
}

/// Parses the Windows `ProxyServer` string: either one `host:port` for every
/// scheme or `http=...;https=...;socks=...` in any order or subset. A scheme
/// without its own entry falls back to the socks proxy.
pub fn parse_windows_proxy(value: &str) -> (Option<Endpoint>, Option<Endpoint>) {
    let tokens: Vec<&str> = value.split([';', ' ', '\t']).filter(|t| !t.is_empty()).collect();
    if tokens.is_empty() {
        return (None, None);
    }
    if !tokens.iter().any(|t| t.contains('=')) {
        // single address for all schemes
        let e = canonicalize(tokens[0]).ok();
        return (e.clone(), e);
    }
    let (mut http, mut https, mut socks) = (None, None, None);
    for token in tokens {
        let Some((scheme, addr)) = token.split_once('=') else {
            continue;
        };
        match scheme.to_ascii_lowercase().as_str() {
            "http" => http = canonicalize(addr).ok().or(http),
            "https" => https = canonicalize(addr).ok().or(https),
            "socks" | "socks5" => {
                socks = canonicalize(&format!("socks5://{}", strip_scheme_prefix(addr)))
                    .ok()
                    .or(socks)
            }
            _ => {}
        }
    }
    (http.or_else(|| socks.clone()), https.or(socks))
}

/// Builds the detection from the pieces Windows exposes in the current user's
/// Internet Settings.
pub fn from_windows_values(enabled: bool, server: &str, override_list: &str, auto_config_url: &str) -> Detected {
    let (http, https) = if enabled {
        parse_windows_proxy(server)
    } else {
        (None, None)
    };
    let have = http.is_some() || https.is_some();
    Detected {
        bypass: if have {
            bypass::normalize(override_list)
        } else {
            String::new()
        },
        http,
        https,
        pac: !auto_config_url.trim().is_empty(),
    }
}

#[cfg(windows)]
pub fn read_os() -> Detected {
    use winreg::enums::HKEY_CURRENT_USER;
    use winreg::RegKey;
    let hkcu = RegKey::predef(HKEY_CURRENT_USER);
    let Ok(key) = hkcu.open_subkey(r"Software\Microsoft\Windows\CurrentVersion\Internet Settings") else {
        return Detected::default();
    };
    let enabled = key.get_value::<u32, _>("ProxyEnable").unwrap_or(0) != 0;
    let server = key.get_value::<String, _>("ProxyServer").unwrap_or_default();
    let over = key.get_value::<String, _>("ProxyOverride").unwrap_or_default();
    let pac = key.get_value::<String, _>("AutoConfigURL").unwrap_or_default();
    from_windows_values(enabled, &server, &over, &pac)
}
