use super::*;
use crate::policy::{Policy, ProxyPolicy};
use address::Credentials;
use settings::{ManualScheme, Mode, Settings};
use std::io::{Read, Write};
use std::net::TcpListener;

fn ep(s: &str) -> Endpoint {
    address::canonicalize(s).unwrap()
}

fn detected(http: Option<&str>, bypass: &str) -> os::Detected {
    os::Detected {
        http: http.map(ep),
        https: http.map(ep),
        bypass: bypass.to_string(),
        pac: false,
    }
}

fn manual(user: &str) -> Settings {
    Settings {
        mode: Mode::Manual,
        scheme: ManualScheme::Http,
        host: "proxy.corp".into(),
        port: Some(3128),
        username: user.into(),
        bypass: "10.*;.corp".into(),
    }
}

fn run(
    policy: &ProxyPolicy,
    s: &Settings,
    pw: Option<&str>,
    env: &os::Detected,
    osd: &os::Detected,
    wv: bool,
) -> Effective {
    resolve(&Inputs {
        policy,
        settings: s,
        password: pw,
        environment: env,
        os: osd,
        webview_configurable: wv,
    })
}

#[test]
fn policy_beats_everything() {
    let none = os::Detected::default();
    let e = run(
        &ProxyPolicy::Direct,
        &manual(""),
        None,
        &detected(Some("e:1"), ""),
        &none,
        true,
    );
    assert_eq!(e.source, Source::Policy);
    assert!(e.http.is_none());
    assert_eq!(e.webview, WebviewProxy::Direct);

    let fixed = ProxyPolicy::Fixed {
        endpoint: ep("fx:9"),
        bypass: bypass::normalize(".lan"),
    };
    let e = run(&fixed, &Settings::default(), None, &none, &none, true);
    assert_eq!(e.source, Source::Policy);
    assert_eq!(e.http.as_ref().unwrap().address(), "http://fx:9");
    assert_eq!(e.https.as_ref().unwrap().address(), "http://fx:9");
    assert_eq!(e.webview, WebviewProxy::Fixed);

    // policy "system" ignores the app setting
    let e = run(
        &ProxyPolicy::System,
        &manual(""),
        None,
        &detected(Some("env:1"), ""),
        &none,
        true,
    );
    assert_eq!(e.source, Source::Environment);
}

#[test]
fn setting_then_environment_then_os() {
    let none = os::Detected::default();
    let env = detected(Some("env:1"), "x");
    let osd = detected(Some("os:2"), "y");
    let e = run(&ProxyPolicy::None, &Settings::default(), None, &env, &osd, true);
    assert_eq!(e.source, Source::Environment);
    let e = run(&ProxyPolicy::None, &Settings::default(), None, &none, &osd, true);
    assert_eq!(e.source, Source::System);
    assert_eq!(e.http.as_ref().unwrap().address(), "http://os:2");
    let e = run(&ProxyPolicy::None, &Settings::default(), None, &none, &none, true);
    assert_eq!(e.source, Source::None);
    let direct = Settings {
        mode: Mode::Direct,
        ..Settings::default()
    };
    let e = run(&ProxyPolicy::None, &direct, None, &env, &osd, true);
    assert_eq!(e.source, Source::Direct);
    assert!(e.http.is_none());
}

#[test]
fn manual_with_password_and_webview_fallback() {
    let none = os::Detected::default();
    let e = run(
        &ProxyPolicy::None,
        &manual("alice"),
        Some("p@ss:w/rd"),
        &none,
        &none,
        true,
    );
    assert_eq!(e.source, Source::Manual);
    assert_eq!(
        e.http.as_ref().unwrap().url_string(),
        "http://alice:p%40ss%3Aw%2Frd@proxy.corp:3128/"
    );
    assert_eq!(e.webview, WebviewProxy::System);
    assert!(e.webview_follows_system);
    assert_eq!(webview_args(&e), "");

    let e = run(&ProxyPolicy::None, &manual(""), None, &none, &none, true);
    assert_eq!(e.webview, WebviewProxy::Fixed);
    assert!(!e.webview_follows_system);
    let args = webview_args(&e);
    assert!(args.starts_with("--proxy-server=http://proxy.corp:3128 --proxy-bypass-list=10.0.0.0/8;.corp "));
    assert!(args.contains("--disable-features=msWebOOUI,msPdfOOUI,msSmartScreenProtection"));
    assert!(!args.contains("alice"));

    // non-Windows webviews always follow the OS
    let e = run(&ProxyPolicy::None, &manual(""), None, &none, &none, false);
    assert_eq!(e.webview, WebviewProxy::System);
    assert!(e.webview_follows_system);
}

#[test]
fn incomplete_manual_falls_back_to_system_never_direct() {
    let none = os::Detected::default();
    let mut s = manual("");
    s.port = None;
    let osd = detected(Some("os:2"), "");
    let e = run(&ProxyPolicy::None, &s, None, &none, &osd, true);
    assert_eq!(e.source, Source::System);
}

#[test]
fn direct_webview_args() {
    let none = os::Detected::default();
    let direct = Settings {
        mode: Mode::Direct,
        ..Settings::default()
    };
    let e = run(&ProxyPolicy::None, &direct, None, &none, &none, true);
    let args = webview_args(&e);
    assert!(args.starts_with("--no-proxy-server "));
    assert!(args.contains("--disable-features="));
}

#[test]
fn pac_only_is_reported_and_ignored() {
    let none = os::Detected::default();
    let pac = os::Detected {
        pac: true,
        ..Default::default()
    };
    let e = run(&ProxyPolicy::None, &Settings::default(), None, &none, &pac, true);
    assert_eq!(e.source, Source::None);
    assert!(e.pac_ignored);
}
