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

/// Parses the output of `scutil --proxy` (the system dynamic store).
pub fn parse_scutil(output: &str) -> Detected {
    use std::collections::HashMap;
    let mut flat: HashMap<String, String> = HashMap::new();
    let mut exceptions: Vec<String> = Vec::new();
    let mut in_exceptions = false;
    for line in output.lines() {
        let t = line.trim();
        if in_exceptions {
            if t.starts_with('}') {
                in_exceptions = false;
            } else if let Some((_, v)) = t.split_once(" : ") {
                exceptions.push(v.trim().to_string());
            }
            continue;
        }
        let Some((k, v)) = t.split_once(" : ") else {
            continue;
        };
        if k == "ExceptionsList" {
            in_exceptions = true;
            continue;
        }
        flat.insert(k.trim().to_string(), v.trim().to_string());
    }
    let on = |k: &str| flat.get(k).map(|v| v == "1").unwrap_or(false);
    let endpoint = |prefix: &str, socks: bool| -> Option<Endpoint> {
        if !on(&format!("{prefix}Enable")) {
            return None;
        }
        let host = flat.get(&format!("{prefix}Proxy"))?;
        let port = flat.get(&format!("{prefix}Port"))?;
        let addr = if socks {
            format!("socks5://{host}:{port}")
        } else {
            format!("http://{host}:{port}")
        };
        canonicalize(&addr).ok()
    };
    let socks = endpoint("SOCKS", true);
    let http = endpoint("HTTP", false).or_else(|| socks.clone());
    let https = endpoint("HTTPS", false).or(socks);
    let have = http.is_some() || https.is_some();
    Detected {
        bypass: if have {
            bypass::normalize(&exceptions.join(","))
        } else {
            String::new()
        },
        http,
        https,
        pac: on("ProxyAutoConfigEnable"),
    }
}

#[cfg(target_os = "macos")]
pub fn read_os() -> Detected {
    match std::process::Command::new("/usr/sbin/scutil").arg("--proxy").output() {
        Ok(out) if out.status.success() => parse_scutil(&String::from_utf8_lossy(&out.stdout)),
        _ => Detected::default(),
    }
}

#[cfg(not(any(windows, target_os = "macos")))]
pub fn read_os() -> Detected {
    Detected::default()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn env(pairs: &[(&str, &str)]) -> impl Fn(&str) -> Option<String> {
        let owned: Vec<(String, String)> = pairs.iter().map(|(k, v)| (k.to_string(), v.to_string())).collect();
        move |name: &str| owned.iter().find(|(k, _)| k == name).map(|(_, v)| v.clone())
    }

    #[test]
    fn environment_prefers_specific_over_all() {
        let d = from_environment(env(&[
            ("HTTP_PROXY", "http://a:1"),
            ("ALL_PROXY", "socks5://b:2"),
            ("NO_PROXY", "intra.corp"),
        ]));
        assert_eq!(d.http.unwrap().address(), "http://a:1");
        assert_eq!(d.https.unwrap().address(), "socks5://b:2");
        assert_eq!(d.bypass, "intra.corp,localhost,127.0.0.0/8,::1");
    }

    #[test]
    fn environment_accepts_lowercase_and_ignores_garbage() {
        let d = from_environment(env(&[("https_proxy", "p.corp:8080"), ("http_proxy", "https://x:1")]));
        assert!(d.http.is_none());
        assert_eq!(d.https.unwrap().address(), "http://p.corp:8080");
        let none = from_environment(env(&[("NO_PROXY", "a")]));
        assert!(!none.has_proxy());
        assert_eq!(none.bypass, "");
    }

    #[test]
    fn windows_single_and_per_scheme() {
        let (h, s) = parse_windows_proxy("proxy.corp:3128");
        assert_eq!(h.unwrap().address(), "http://proxy.corp:3128");
        assert_eq!(s.unwrap().address(), "http://proxy.corp:3128");

        let (h, s) = parse_windows_proxy("https=sec:8443; http=plain:8080");
        assert_eq!(h.unwrap().address(), "http://plain:8080");
        assert_eq!(s.unwrap().address(), "http://sec:8443");

        let (h, s) = parse_windows_proxy("socks=sx:1080;ftp=ignored:21");
        assert_eq!(h.unwrap().address(), "socks5://sx:1080");
        assert_eq!(s.unwrap().address(), "socks5://sx:1080");

        let (h, s) = parse_windows_proxy("http=only:80\tsocks=sx:1080");
        assert_eq!(h.unwrap().address(), "http://only:80");
        assert_eq!(s.unwrap().address(), "socks5://sx:1080");
        assert_eq!(parse_windows_proxy(""), (None, None));
    }

    #[test]
    fn windows_values_and_pac() {
        let d = from_windows_values(true, "p:1", "10.*;<local>", "");
        assert!(d.has_proxy());
        assert_eq!(d.bypass, "10.0.0.0/8,localhost,127.0.0.0/8,::1");
        assert!(!d.pac);
        let d = from_windows_values(false, "p:1", "", "http://wpad/proxy.pac");
        assert!(!d.has_proxy());
        assert!(d.pac);
    }

    const SCUTIL: &str = r#"<dictionary> {
  ExceptionsList : <array> {
    0 : *.local
    1 : 169.254/16
  }
  ExcludeSimpleHostnames : 1
  FTPPassive : 1
  HTTPEnable : 1
  HTTPPort : 8080
  HTTPProxy : proxy.corp
  HTTPSEnable : 0
  ProxyAutoConfigEnable : 0
  SOCKSEnable : 1
  SOCKSPort : 1080
  SOCKSProxy : sx.corp
}
"#;

    #[test]
    fn scutil_output() {
        let d = parse_scutil(SCUTIL);
        assert_eq!(d.http.unwrap().address(), "http://proxy.corp:8080");
        // https falls back to the socks proxy
        assert_eq!(d.https.unwrap().address(), "socks5://sx.corp:1080");
        assert_eq!(d.bypass, ".local,169.254.0.0/16,localhost,127.0.0.0/8,::1");
        assert!(!d.pac);
    }

    #[test]
    fn scutil_pac_only() {
        let d = parse_scutil(
            "<dictionary> {\n  ProxyAutoConfigEnable : 1\n  ProxyAutoConfigURLString : http://x/p.pac\n}\n",
        );
        assert!(!d.has_proxy());
        assert!(d.pac);
    }
}
