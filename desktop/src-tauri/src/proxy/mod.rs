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

/// A cloneable routing table for shell-originated HTTP clients.
#[derive(Debug, Clone)]
pub struct Route {
    http: Option<Endpoint>,
    https: Option<Endpoint>,
    bypass: BypassList,
}

impl Route {
    pub fn from_effective(effective: &Effective) -> Self {
        Self {
            http: effective.http.clone(),
            https: effective.https.clone(),
            bypass: BypassList::parse(&effective.bypass),
        }
    }

    pub fn direct() -> Self {
        Self {
            http: None,
            https: None,
            bypass: BypassList::default(),
        }
    }

    /// The proxy for a destination, or `None` for a direct connection.
    /// Loopback destinations are never proxied.
    pub fn proxy_for(&self, destination: &url::Url) -> Option<Endpoint> {
        if crate::origin::is_loopback_url(destination) {
            return None;
        }
        let host = destination.host_str()?;
        if self.bypass.matches(host, destination.port_or_known_default()) {
            return None;
        }
        match destination.scheme() {
            "http" => self.http.clone(),
            "https" => self.https.clone(),
            _ => None,
        }
    }

    /// A client builder that follows exactly this route (the library's own
    /// system proxy detection is switched off).
    pub fn client_builder(&self) -> reqwest::ClientBuilder {
        let route = self.clone();
        reqwest::Client::builder()
            .no_proxy()
            .proxy(reqwest::Proxy::custom(move |url| {
                route.proxy_for(url).and_then(|e| e.to_url())
            }))
    }
}

/// Status reported by `get_network_proxy`.
#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Status {
    pub settings: Settings,
    pub has_password: bool,
    pub managed_by_policy: bool,
    pub restart_required: bool,
    pub system_changed: bool,
    pub effective: EffectiveView,
}

#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct EffectiveView {
    pub source: Source,
    pub http: Option<String>,
    pub https: Option<String>,
    pub bypass: String,
    pub pac_ignored: bool,
    pub webview: WebviewProxy,
    pub webview_follows_system: bool,
}

impl EffectiveView {
    pub fn from(e: &Effective) -> Self {
        Self {
            source: e.source,
            http: e.http.as_ref().map(|x| x.address()),
            https: e.https.as_ref().map(|x| x.address()),
            bypass: e.bypass.clone(),
            pac_ignored: e.pac_ignored,
            webview: e.webview,
            webview_follows_system: e.webview_follows_system,
        }
    }
}

/// The proxy state captured once at startup.
pub struct State {
    pub policy: ProxyPolicy,
    /// Settings in use for this run.
    pub in_use: Settings,
    /// Password in use for this run (never logged).
    password: Option<String>,
    environment: os::Detected,
    os: os::Detected,
    pub effective: Effective,
    follows_system: bool,
}

impl std::fmt::Debug for State {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("State")
            .field("in_use", &self.in_use)
            .field("password", &self.password.as_ref().map(|_| "<redacted>"))
            .field("effective", &self.effective)
            .finish()
    }
}

impl State {
    pub fn capture(
        policy: &Policy,
        settings: Settings,
        password: Option<String>,
        environment: os::Detected,
        os: os::Detected,
        webview_configurable: bool,
    ) -> Self {
        // the password is only used for a manual proxy with a user name
        let pw = if settings.mode == Mode::Manual && !settings.username.trim().is_empty() {
            password
        } else {
            None
        };
        let effective = resolve(&Inputs {
            policy: &policy.proxy,
            settings: &settings,
            password: pw.as_deref(),
            environment: &environment,
            os: &os,
            webview_configurable,
        });
        let follows_system = matches!(effective.source, Source::Environment | Source::System | Source::None);
        Self {
            policy: policy.proxy.clone(),
            in_use: settings,
            password: pw,
            environment,
            os,
            effective,
            follows_system,
        }
    }

    pub fn route(&self) -> Route {
        Route::from_effective(&self.effective)
    }

    /// Builds the status object. `saved` and `saved_password` are the values
    /// currently persisted; `live_*` are fresh readings of the environment/OS.
    pub fn status(
        &self,
        saved: &Settings,
        saved_password: Option<&str>,
        live_environment: &os::Detected,
        live_os: &os::Detected,
        managed_by_policy: bool,
    ) -> Status {
        let effective_saved_password = if saved.mode == Mode::Manual && !saved.username.trim().is_empty() {
            saved_password
        } else {
            None
        };
        let restart_required = *saved != self.in_use || effective_saved_password != self.password.as_deref();
        let system_changed = self.follows_system && (*live_environment != self.environment || *live_os != self.os);
        Status {
            settings: saved.clone(),
            has_password: !saved.username.trim().is_empty() && saved_password.map(|p| !p.is_empty()).unwrap_or(false),
            managed_by_policy,
            restart_required,
            system_changed,
            effective: EffectiveView::from(&self.effective),
        }
    }
}

#[cfg(test)]
mod tests;
