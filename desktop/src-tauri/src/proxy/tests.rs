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

#[test]
fn child_environment_has_all_eight_variables() {
    let none = os::Detected::default();
    let e = run(
        &ProxyPolicy::None,
        &manual("alice"),
        Some("p@ss:w/rd"),
        &none,
        &none,
        true,
    );
    let env: std::collections::HashMap<_, _> = child_env(&e).into_iter().collect();
    assert_eq!(env.len(), 8);
    for k in ["HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy"] {
        assert_eq!(env[k], "http://alice:p%40ss%3Aw%2Frd@proxy.corp:3128/");
    }
    for k in ["NO_PROXY", "no_proxy"] {
        assert_eq!(env[k], "10.0.0.0/8,.corp,localhost,127.0.0.0/8,::1");
    }
    assert_eq!(env["ALL_PROXY"], "");
    assert_eq!(env["all_proxy"], "");

    let e = run(&ProxyPolicy::None, &Settings::default(), None, &none, &none, true);
    let env = child_env(&e);
    assert_eq!(env.len(), 8);
    assert!(env.iter().all(|(_, v)| v.is_empty()));
}

#[test]
fn status_flags() {
    let none = os::Detected::default();
    let pol = Policy::default();
    let st = State::capture(&pol, Settings::default(), None, none.clone(), none.clone(), true);
    let s = st.status(&Settings::default(), None, &none, &none, false);
    assert!(!s.restart_required);
    assert!(!s.system_changed);
    assert!(!s.has_password);
    let v = serde_json::to_value(&s).unwrap();
    assert_eq!(v["effective"]["source"], "none");
    assert_eq!(v["effective"]["webview"], "system");
    assert!(v["effective"]["pacIgnored"] == false);

    // the OS gained a proxy since launch
    let live = detected(Some("os:2"), "");
    let s = st.status(&Settings::default(), None, &none, &live, false);
    assert!(s.system_changed);

    // saved settings differ from the ones in use
    let saved = manual("bob");
    let s = st.status(&saved, Some("pw"), &none, &none, false);
    assert!(s.restart_required);
    assert!(s.has_password);
    assert!(!s.system_changed || st.follows_system);

    // password changed while settings stayed the same
    let st2 = State::capture(
        &pol,
        manual("bob"),
        Some("old".into()),
        none.clone(),
        none.clone(),
        true,
    );
    assert!(
        !st2.status(&manual("bob"), Some("old"), &none, &none, false)
            .restart_required
    );
    assert!(
        st2.status(&manual("bob"), Some("new"), &none, &none, false)
            .restart_required
    );
    // a manual proxy is not "following" the system
    let s = st2.status(&manual("bob"), Some("old"), &none, &live, false);
    assert!(!s.system_changed);
}

#[test]
fn state_debug_hides_password() {
    let none = os::Detected::default();
    let st = State::capture(
        &Policy::default(),
        manual("bob"),
        Some("hunter2".into()),
        none.clone(),
        none,
        true,
    );
    assert!(!format!("{st:?}").contains("hunter2"));
}

#[test]
fn route_honours_bypass_and_loopback() {
    let none = os::Detected::default();
    let e = run(&ProxyPolicy::None, &manual("u"), Some("p"), &none, &none, true);
    let route = Route::from_effective(&e);
    let u = |s: &str| url::Url::parse(s).unwrap();
    assert!(route.proxy_for(&u("https://chat.example.com/")).is_some());
    assert!(route.proxy_for(&u("http://chat.example.com/")).is_some());
    assert!(route.proxy_for(&u("https://wiki.corp/")).is_none());
    assert!(route.proxy_for(&u("http://10.1.2.3:8080/")).is_none());
    assert!(route.proxy_for(&u("http://127.0.0.1:9/")).is_none());
    assert!(route.proxy_for(&u("http://localhost:9/")).is_none());
    assert!(Route::direct().proxy_for(&u("https://x.y/")).is_none());
}

fn http_request(client: &reqwest::Client, url: &str) -> Result<String, reqwest::Error> {
    let client = client.clone();
    let url = url.to_string();
    tauri::async_runtime::block_on(async move { client.get(&url).send().await?.text().await })
}

#[test]
fn plain_http_goes_through_the_proxy_in_absolute_form_with_basic_auth() {
    // a stub proxy that records the request head and answers 200
    let stub = TcpListener::bind("127.0.0.1:0").unwrap();
    let port = stub.local_addr().unwrap().port();
    let handle = std::thread::spawn(move || {
        let (mut conn, _) = stub.accept().unwrap();
        let mut buf = [0u8; 4096];
        let n = conn.read(&mut buf).unwrap();
        let head = String::from_utf8_lossy(&buf[..n]).to_string();
        conn.write_all(b"HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok")
            .unwrap();
        head
    });
    let eff = Effective {
        source: Source::Manual,
        http: Some(
            ep(&format!("http://127.0.0.1:{port}")).with_credentials(Some(Credentials {
                username: "alice".into(),
                password: "secret".into(),
            })),
        ),
        https: None,
        bypass: bypass::normalize(""),
        pac_ignored: false,
        webview: WebviewProxy::System,
        webview_follows_system: true,
    };
    // 127.0.0.1 would be bypassed as loopback, so use a name that is not
    // loopback but resolves locally through the proxy: the proxy sees the
    // absolute URI, the name never needs to resolve.
    let route = Route::from_effective(&eff);
    let client = route.client_builder().build().unwrap();
    let body = http_request(&client, "http://upstream.example.test/path?q=1").unwrap();
    assert_eq!(body, "ok");
    let head = handle.join().unwrap();
    assert!(
        head.starts_with("GET http://upstream.example.test/path?q=1 HTTP/1.1"),
        "{head}"
    );
    let lower = head.to_ascii_lowercase();
    // base64("alice:secret")
    assert!(lower.contains("proxy-authorization: basic ywxpy2u6c2vjcmv0"), "{head}");
}

#[test]
fn bypassed_and_loopback_hosts_never_reach_the_proxy() {
    let stub = TcpListener::bind("127.0.0.1:0").unwrap();
    let proxy_port = stub.local_addr().unwrap().port();
    stub.set_nonblocking(true).unwrap();
    let origin = TcpListener::bind("127.0.0.1:0").unwrap();
    let origin_port = origin.local_addr().unwrap().port();
    std::thread::spawn(move || {
        if let Ok((mut c, _)) = origin.accept() {
            let mut buf = [0u8; 1024];
            let _ = c.read(&mut buf);
            let _ = c.write_all(b"HTTP/1.1 200 OK\r\nContent-Length: 6\r\nConnection: close\r\n\r\ndirect");
        }
    });
    let eff = Effective {
        source: Source::Manual,
        http: Some(ep(&format!("http://127.0.0.1:{proxy_port}"))),
        https: Some(ep(&format!("http://127.0.0.1:{proxy_port}"))),
        bypass: bypass::normalize(""),
        pac_ignored: false,
        webview: WebviewProxy::System,
        webview_follows_system: true,
    };
    let client = Route::from_effective(&eff).client_builder().build().unwrap();
    let body = http_request(&client, &format!("http://127.0.0.1:{origin_port}/")).unwrap();
    assert_eq!(body, "direct");
    assert!(stub.accept().is_err(), "the proxy must not have been contacted");
}

#[test]
fn socks_addresses_build_a_client() {
    let eff = Effective {
        source: Source::Manual,
        http: Some(ep("socks5://127.0.0.1:1080")),
        https: Some(ep("socks5://127.0.0.1:1080")),
        bypass: bypass::normalize(""),
        pac_ignored: false,
        webview: WebviewProxy::System,
        webview_follows_system: true,
    };
    assert!(Route::from_effective(&eff).client_builder().build().is_ok());
}
