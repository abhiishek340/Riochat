//! The user's proxy preference (`network.json`) and its validation.

use super::address::{self, Credentials, Endpoint, Scheme};
use super::bypass;
use serde::{Deserialize, Serialize};
use std::path::Path;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Mode {
    #[default]
    System,
    Manual,
    Direct,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum ManualScheme {
    #[default]
    Http,
    Socks5,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct Settings {
    pub mode: Mode,
    pub scheme: ManualScheme,
    pub host: String,
    pub port: Option<u16>,
    pub username: String,
    pub bypass: String,
}

/// Error returned by `set_network_proxy`.
#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ProxyError {
    pub kind: &'static str,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub field: Option<&'static str>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub value: Option<String>,
    pub message: String,
}

impl ProxyError {
    pub fn invalid(field: &'static str, value: Option<String>, message: impl Into<String>) -> Self {
        Self {
            kind: "invalid",
            field: Some(field),
            value,
            message: message.into(),
        }
    }

    pub fn storage(message: impl Into<String>) -> Self {
        Self {
            kind: "storage",
            field: None,
            value: None,
            message: message.into(),
        }
    }

    pub fn policy() -> Self {
        Self {
            kind: "policy",
            field: None,
            value: None,
            message: "policy: the network proxy is managed by your administrator".to_string(),
        }
    }
}

impl Settings {
    /// The manual proxy as an endpoint, `None` when the fields are incomplete
    /// (for example a hand-edited file).
    pub fn manual_endpoint(&self) -> Option<Endpoint> {
        if self.mode != Mode::Manual {
            return None;
        }
        let port = self.port.filter(|p| *p != 0)?;
        let host = self.host.trim();
        if host.is_empty() {
            return None;
        }
        let bracketed = if host.contains(':') && !host.starts_with('[') {
            format!("[{host}]")
        } else {
            host.to_string()
        };
        let scheme = match self.scheme {
            ManualScheme::Http => Scheme::Http,
            ManualScheme::Socks5 => Scheme::Socks5,
        };
        address::canonicalize(&format!("{}://{}:{}", scheme.as_str(), bracketed, port)).ok()
    }

    pub fn credentials(&self, password: Option<&str>) -> Option<Credentials> {
        let user = self.username.trim();
        if user.is_empty() {
            return None;
        }
        Some(Credentials {
            username: user.to_string(),
            password: password.unwrap_or("").to_string(),
        })
    }
}

/// Validates and trims settings as typed by the user.
pub fn validate(input: &Settings) -> Result<Settings, ProxyError> {
    let mut s = input.clone();
    s.host = s.host.trim().to_string();
    s.username = s.username.trim().to_string();
    s.bypass = s.bypass.trim().to_string();
    if s.mode != Mode::Manual {
        // inactive fields are kept so switching back restores them
        return Ok(s);
    }
    match s.port {
        Some(p) if p >= 1 => {}
        _ => return Err(ProxyError::invalid("port", None, "enter the proxy port")),
    }
    let host_message = "enter a host name or IP address without a scheme or path";
    if s.host.is_empty() || s.host.chars().any(|c| c.is_whitespace() || "/@?#".contains(c)) {
        return Err(ProxyError::invalid("host", None, host_message));
    }
    if s.manual_endpoint().is_none() {
        return Err(ProxyError::invalid("host", None, host_message));
    }
    if let Some(bad) = bypass::first_invalid(&s.bypass) {
        return Err(ProxyError::invalid(
            "bypass",
            Some(bad.clone()),
            format!("\"{bad}\" is not a valid host, address or range"),
        ));
    }
    Ok(s)
}

/// Reads `network.json`. A missing or unreadable file yields defaults.
pub fn load(path: &Path) -> Settings {
    match std::fs::read(path) {
        Ok(bytes) => serde_json::from_slice(&bytes).unwrap_or_else(|e| {
            log::warn!("network.json is unreadable ({e}); using defaults");
            Settings::default()
        }),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => Settings::default(),
        Err(e) => {
            log::warn!("cannot read network.json ({e}); using defaults");
            Settings::default()
        }
    }
}

pub fn save(path: &Path, settings: &Settings) -> Result<(), ProxyError> {
    let mut bytes = serde_json::to_vec_pretty(settings)
        .map_err(|e| ProxyError::storage(format!("cannot encode network settings: {e}")))?;
    bytes.push(b'\n');
    crate::fsutil::atomic_write(path, &bytes)
        .map_err(|e| ProxyError::storage(format!("cannot write network settings: {e}")))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn manual(host: &str, port: Option<u16>, bypass: &str) -> Settings {
        Settings {
            mode: Mode::Manual,
            host: host.into(),
            port,
            bypass: bypass.into(),
            ..Settings::default()
        }
    }

    #[test]
    fn defaults_serialize_as_documented() {
        let v = serde_json::to_value(Settings::default()).unwrap();
        assert_eq!(v["mode"], "system");
        assert_eq!(v["scheme"], "http");
        assert_eq!(v["host"], "");
        assert!(v["port"].is_null());
        assert_eq!(v["username"], "");
        assert_eq!(v["bypass"], "");
    }

    #[test]
    fn non_manual_is_accepted_as_typed() {
        let s = Settings {
            mode: Mode::Direct,
            host: " bad host/x ".into(),
            ..Settings::default()
        };
        let out = validate(&s).unwrap();
        assert_eq!(out.mode, Mode::Direct);
        assert_eq!(out.host, "bad host/x");
    }

    #[test]
    fn manual_validation_fields() {
        assert_eq!(validate(&manual("p.corp", None, "")).unwrap_err().field, Some("port"));
        assert_eq!(
            validate(&manual("p.corp", Some(0), "")).unwrap_err().field,
            Some("port")
        );
        for host in ["", "http://p.corp", "p.corp/x", "u@p", "a b"] {
            assert_eq!(
                validate(&manual(host, Some(80), "")).unwrap_err().field,
                Some("host"),
                "{host:?}"
            );
        }
        let e = validate(&manual("p.corp", Some(80), "a.corp;bad_one")).unwrap_err();
        assert_eq!(e.field, Some("bypass"));
        assert_eq!(e.value.as_deref(), Some("bad_one"));
        assert!(validate(&manual("  p.corp ", Some(3128), "10.* <local>")).is_ok());
        assert!(validate(&manual("::1", Some(3128), "")).is_ok());
    }

    #[test]
    fn manual_endpoint_brackets_ipv6() {
        let e = manual("fe80::1", Some(8080), "").manual_endpoint().unwrap();
        assert_eq!(e.address(), "http://[fe80::1]:8080");
        assert!(manual("", Some(80), "").manual_endpoint().is_none());
        assert!(manual("h", None, "").manual_endpoint().is_none());
    }

    #[test]
    fn file_round_trip_and_corrupt_file() {
        let dir = tempfile::tempdir().unwrap();
        let p = dir.path().join("network.json");
        assert_eq!(load(&p), Settings::default());
        let s = manual("p.corp", Some(3128), "10.*");
        save(&p, &s).unwrap();
        assert_eq!(load(&p), s);
        std::fs::write(&p, b"{not json").unwrap();
        assert_eq!(load(&p), Settings::default());
        let text = {
            save(&p, &s).unwrap();
            std::fs::read_to_string(&p).unwrap()
        };
        assert!(text.contains("\n  \"mode\""), "pretty printed");
    }
}
