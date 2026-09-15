//! Network proxy: one decision per launch, shared by the sidecar environment,
//! shell HTTP clients and (on Windows) webview arguments.

pub mod address;
pub mod bypass;
pub mod os;
pub mod settings;

use crate::policy::{Policy, ProxyPolicy};
use address::Endpoint;
use bypass::BypassList;
use serde::Serialize;
use settings::{Mode, Settings};

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "lowercase")]
pub enum Source {
    Policy,
    Manual,
    Direct,
    Environment,
    System,
    None,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "lowercase")]
pub enum WebviewProxy {
    System,
    Fixed,
    Direct,
}

/// The routing decision.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Effective {
    pub source: Source,
    pub http: Option<Endpoint>,
    pub https: Option<Endpoint>,
    /// Normalized list; empty when no proxy is in use.
    pub bypass: String,
    pub pac_ignored: bool,
    pub webview: WebviewProxy,
    pub webview_follows_system: bool,
}

/// Everything the decision depends on.
pub struct Inputs<'a> {
    pub policy: &'a ProxyPolicy,
    pub settings: &'a Settings,
    pub password: Option<&'a str>,
    pub environment: &'a os::Detected,
    pub os: &'a os::Detected,
    /// Whether this platform can configure webviews explicitly (Windows).
    pub webview_configurable: bool,
}

fn system_path(inputs: &Inputs<'_>) -> Effective {
    if inputs.environment.has_proxy() {
        return Effective {
            source: Source::Environment,
            http: inputs.environment.http.clone(),
            https: inputs.environment.https.clone(),
            bypass: inputs.environment.bypass.clone(),
            pac_ignored: false,
            webview: WebviewProxy::System,
            webview_follows_system: false,
        };
    }
    if inputs.os.has_proxy() {
        return Effective {
            source: Source::System,
            http: inputs.os.http.clone(),
            https: inputs.os.https.clone(),
            bypass: inputs.os.bypass.clone(),
            pac_ignored: false,
            webview: WebviewProxy::System,
            webview_follows_system: false,
        };
    }
    Effective {
        source: Source::None,
        http: None,
        https: None,
        bypass: String::new(),
        pac_ignored: inputs.os.pac,
        webview: WebviewProxy::System,
        webview_follows_system: false,
    }
}

fn direct(source: Source, inputs: &Inputs<'_>) -> Effective {
    Effective {
        source,
        http: None,
        https: None,
        bypass: String::new(),
        pac_ignored: false,
        webview: if inputs.webview_configurable {
            WebviewProxy::Direct
        } else {
            WebviewProxy::System
        },
        webview_follows_system: !inputs.webview_configurable,
    }
}

/// Resolves the decision. Precedence: policy, app setting, environment, OS.
pub fn resolve(inputs: &Inputs<'_>) -> Effective {
    match inputs.policy {
        ProxyPolicy::Direct => return direct(Source::Policy, inputs),
        ProxyPolicy::Fixed { endpoint, bypass } => {
            return Effective {
                source: Source::Policy,
                http: Some(endpoint.clone()),
                https: Some(endpoint.clone()),
                bypass: bypass.clone(),
                pac_ignored: false,
                webview: if inputs.webview_configurable {
                    WebviewProxy::Fixed
                } else {
                    WebviewProxy::System
                },
                webview_follows_system: !inputs.webview_configurable,
            };
        }
        ProxyPolicy::System => return system_path(inputs),
        ProxyPolicy::None => {}
    }
    match inputs.settings.mode {
        Mode::Direct => direct(Source::Direct, inputs),
        Mode::System => system_path(inputs),
        Mode::Manual => match inputs.settings.manual_endpoint() {
            // an incomplete hand-edited manual setting falls back to the
            // system path, never to direct
            None => system_path(inputs),
            Some(endpoint) => {
                let creds = inputs.settings.credentials(inputs.password);
                let needs_password = creds.is_some();
                let configurable = inputs.webview_configurable && !needs_password;
                Effective {
                    source: Source::Manual,
                    http: Some(endpoint.clone().with_credentials(creds.clone())),
                    https: Some(endpoint.with_credentials(creds)),
                    bypass: bypass::normalize(&inputs.settings.bypass),
                    pac_ignored: false,
                    webview: if configurable {
                        WebviewProxy::Fixed
                    } else {
                        WebviewProxy::System
                    },
                    webview_follows_system: !configurable,
                }
            }
        },
    }
}

const ENGINE_DEFAULT_FEATURES: &str = "--disable-features=msWebOOUI,msPdfOOUI,msSmartScreenProtection";

/// WebView2 browser arguments. Identical for every webview; credentials never
/// appear in them.
pub fn webview_args(effective: &Effective) -> String {
    match effective.webview {
        WebviewProxy::System => String::new(),
        WebviewProxy::Direct => format!("--no-proxy-server {ENGINE_DEFAULT_FEATURES}"),
        WebviewProxy::Fixed => {
            let Some(endpoint) = effective.https.as_ref().or(effective.http.as_ref()) else {
                return String::new();
            };
            let mut args = format!("--proxy-server={}", endpoint.address());
            let list = bypass::without_loopback_semicolon(&effective.bypass);
            if !list.is_empty() {
                args.push_str(&format!(" --proxy-bypass-list={list}"));
            }
            args.push(' ');
            args.push_str(ENGINE_DEFAULT_FEATURES);
            args
        }
    }
}

/// The eight proxy variables for the sidecar. `ALL_PROXY` is always empty and
/// every variable is empty when there is no proxy, so nothing inherited from
/// the parent environment survives.
pub fn child_env(effective: &Effective) -> Vec<(String, String)> {
    let http = effective.http.as_ref().map(|e| e.url_string()).unwrap_or_default();
    let https = effective.https.as_ref().map(|e| e.url_string()).unwrap_or_default();
    let have = effective.http.is_some() || effective.https.is_some();
    let no_proxy = if have { effective.bypass.clone() } else { String::new() };
    let mut out = Vec::with_capacity(8);
    for (upper, lower, v) in [
        ("HTTP_PROXY", "http_proxy", &http),
        ("HTTPS_PROXY", "https_proxy", &https),
        ("NO_PROXY", "no_proxy", &no_proxy),
        ("ALL_PROXY", "all_proxy", &String::new()),
    ] {
        out.push((upper.to_string(), v.clone()));
        out.push((lower.to_string(), v.clone()));
    }
    out
}
