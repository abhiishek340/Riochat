//! HTTP calls the shell makes on behalf of a tab: refresh-token rotation and
//! the local one-time grant exchange.

use crate::error::{ErrorKind, ShellError};
use crate::proxy::Route;
use serde::Deserialize;
use std::fmt;
use std::time::Duration;

pub const REFRESH_PATH: &str = "/api/v1/auth/refresh";
pub const LOCAL_EXCHANGE_PATH: &str = "/api/v1/auth/local/exchange";
pub const MAX_BODY: usize = 256 * 1024;
pub const TIMEOUT: Duration = Duration::from_secs(15);

/// Short-lived credentials handed to the page.
#[derive(Clone, serde::Serialize)]
pub struct Credentials {
    #[serde(rename = "accessToken")]
    pub access_token: String,
    #[serde(rename = "sessionID")]
    pub session_id: String,
}

impl fmt::Debug for Credentials {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("Credentials")
            .field("access_token", &"<redacted>")
            .field("session_id", &self.session_id)
            .finish()
    }
}

/// What a successful call returned: the credentials plus a rotated refresh
/// token when the server issued one.
pub struct Outcome {
    pub credentials: Credentials,
    pub rotated_refresh_token: Option<String>,
}

impl fmt::Debug for Outcome {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("Outcome")
            .field("credentials", &self.credentials)
            .field(
                "rotated_refresh_token",
                &self.rotated_refresh_token.as_ref().map(|_| "<redacted>"),
            )
            .finish()
    }
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct Envelope {
    #[serde(default)]
    error_msg: String,
    #[serde(default)]
    error_code: Option<String>,
    #[serde(default)]
    data: Option<Data>,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct Data {
    #[serde(default)]
    access_token: String,
    #[serde(default, rename = "sessionID")]
    session_id: String,
    #[serde(default)]
    refresh_token: Option<String>,
}

/// Interprets a response. Success needs a 2xx status and a data object; an
/// empty access token is `no_session`.
pub fn parse_response(status: u16, body: &[u8]) -> Result<Outcome, ShellError> {
    let parsed: Option<Envelope> = serde_json::from_slice(body).ok();
    let success = (200..300).contains(&status);
    match (success, parsed) {
        (true, Some(Envelope { data: Some(data), .. })) => {
            if data.access_token.trim().is_empty() {
                return Err(ShellError::no_session());
            }
            Ok(Outcome {
                credentials: Credentials {
                    access_token: data.access_token,
                    session_id: data.session_id,
                },
                rotated_refresh_token: data.refresh_token.filter(|t| !t.trim().is_empty()),
            })
        }
        (_, Some(env)) => {
            let message = if env.error_msg.trim().is_empty() {
                format!("request failed: {status}")
            } else {
                env.error_msg
            };
            Err(ShellError::http(
                status,
                env.error_code.filter(|c| !c.is_empty()),
                message,
            ))
        }
        (_, None) => {
            // not JSON: the body text is the message
            let text = String::from_utf8_lossy(body).trim().to_string();
            let message = if text.is_empty() {
                format!("request failed: {status}")
            } else {
                text
            };
            Err(ShellError::http(status, None, message))
        }
    }
}

async fn post_json(route: &Route, origin: &str, path: &str, payload: serde_json::Value) -> Result<Outcome, ShellError> {
    let url = format!("{origin}{path}");
    let client = route
        .client_builder()
        .redirect(reqwest::redirect::Policy::none())
        .timeout(TIMEOUT)
        .build()
        .map_err(|e| ShellError::network(format!("cannot create HTTP client: {e}")))?;
    let mut response = client
        .post(&url)
        .header("X-Client-Platform", "desktop")
        .header(reqwest::header::ACCEPT, "application/json")
        .json(&payload)
        .send()
        .await
        .map_err(|e| ShellError::network(format!("request failed: {}", describe(&e))))?;
    let status = response.status().as_u16();
    let mut body: Vec<u8> = Vec::new();
    loop {
        match response.chunk().await {
            Ok(Some(chunk)) => {
                if body.len() + chunk.len() > MAX_BODY {
                    return Err(ShellError::network("response too large"));
                }
                body.extend_from_slice(&chunk);
            }
            Ok(None) => break,
            Err(e) => return Err(ShellError::network(format!("request failed: {}", describe(&e)))),
        }
    }
    parse_response(status, &body)
}

/// A short, secret-free description of a transport error.
fn describe(e: &reqwest::Error) -> &'static str {
    if e.is_timeout() {
        "timed out"
    } else if e.is_connect() {
        "could not connect"
    } else if e.is_redirect() {
        "unexpected redirect"
    } else {
        "network error"
    }
}

pub async fn refresh(route: &Route, origin: &str, refresh_token: &str) -> Result<Outcome, ShellError> {
    post_json(
        route,
        origin,
        REFRESH_PATH,
        serde_json::json!({ "refreshToken": refresh_token }),
    )
    .await
}

pub async fn exchange_grant(route: &Route, origin: &str, grant: &str) -> Result<Outcome, ShellError> {
    post_json(
        route,
        origin,
        LOCAL_EXCHANGE_PATH,
        serde_json::json!({ "grant": grant }),
    )
    .await
}

/// Convenience used by callers that only care whether the token was rejected.
pub fn is_rejection(e: &ShellError) -> bool {
    e.is_unauthorized() || e.kind == ErrorKind::NoSession
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::{Read, Write};
    use std::net::TcpListener;

    fn ok_body(access: &str, refresh: Option<&str>) -> Vec<u8> {
        serde_json::to_vec(&serde_json::json!({
            "errorMsg": "",
            "errorCode": null,
            "data": { "accessToken": access, "sessionID": "s1", "refreshToken": refresh }
        }))
        .unwrap()
    }

    #[test]
    fn success_with_and_without_rotation() {
        let o = parse_response(200, &ok_body("at", Some("rt2"))).unwrap();
        assert_eq!(o.credentials.access_token, "at");
        assert_eq!(o.credentials.session_id, "s1");
        assert_eq!(o.rotated_refresh_token.as_deref(), Some("rt2"));
        let o = parse_response(200, &ok_body("at", Some(""))).unwrap();
        assert_eq!(o.rotated_refresh_token, None);
        let o = parse_response(200, &ok_body("at", None)).unwrap();
        assert_eq!(o.rotated_refresh_token, None);
    }

    #[test]
    fn credentials_serialize_with_documented_casing() {
        let o = parse_response(200, &ok_body("at", None)).unwrap();
        let v = serde_json::to_value(&o.credentials).unwrap();
        assert_eq!(v["accessToken"], "at");
        assert_eq!(v["sessionID"], "s1");
    }

    #[test]
    fn empty_access_token_is_no_session() {
        let e = parse_response(200, &ok_body("  ", None)).unwrap_err();
        assert_eq!(e.kind, ErrorKind::NoSession);
    }

    #[test]
    fn error_envelope_and_plain_text() {
        let body = br#"{"errorMsg":"bad token","errorCode":"auth.invalid_refresh_token","data":null}"#;
        let e = parse_response(401, body).unwrap_err();
        assert_eq!(e.kind, ErrorKind::Http);
        assert_eq!(e.status, Some(401));
        assert_eq!(e.error_code.as_deref(), Some("auth.invalid_refresh_token"));
        assert_eq!(e.message, "bad token");
        assert!(e.is_unauthorized());
        assert!(is_rejection(&e));

        let e = parse_response(502, b"upstream down\n").unwrap_err();
        assert_eq!(e.message, "upstream down");
        assert_eq!(e.status, Some(502));
        let e = parse_response(500, b"").unwrap_err();
        assert_eq!(e.message, "request failed: 500");
        // 2xx without data is a failure too
        let e = parse_response(200, br#"{"errorMsg":"","data":null}"#).unwrap_err();
        assert_eq!(e.message, "request failed: 200");
    }

    #[test]
    fn debug_hides_tokens() {
        let o = parse_response(200, &ok_body("ACCESS-SECRET", Some("REFRESH-SECRET"))).unwrap();
        let dump = format!("{o:?}");
        assert!(!dump.contains("ACCESS-SECRET"));
        assert!(!dump.contains("REFRESH-SECRET"));
    }

    fn serve_once(response: Vec<u8>) -> (String, std::thread::JoinHandle<String>) {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let origin = format!("http://127.0.0.1:{}", listener.local_addr().unwrap().port());
        let h = std::thread::spawn(move || {
            let (mut c, _) = listener.accept().unwrap();
            let mut buf = Vec::new();
            let mut chunk = [0u8; 2048];
            loop {
                let n = c.read(&mut chunk).unwrap();
                buf.extend_from_slice(&chunk[..n]);
                let text = String::from_utf8_lossy(&buf).to_string();
                if let Some(idx) = text.find("\r\n\r\n") {
                    let len = text[..idx]
                        .lines()
                        .find_map(|l| {
                            l.to_ascii_lowercase()
                                .strip_prefix("content-length:")
                                .map(|v| v.trim().parse::<usize>().unwrap())
                        })
                        .unwrap_or(0);
                    if buf.len() >= idx + 4 + len {
                        break;
                    }
                }
            }
            c.write_all(&response).unwrap();
            String::from_utf8_lossy(&buf).to_string()
        });
        (origin, h)
    }

    fn http_response(status: &str, body: &[u8], extra: &str) -> Vec<u8> {
        let mut out = format!(
            "HTTP/1.1 {status}\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n{extra}\r\n",
            body.len()
        )
        .into_bytes();
        out.extend_from_slice(body);
        out
    }

    #[test]
    fn refresh_sends_platform_header_and_body() {
        let (origin, h) = serve_once(http_response("200 OK", &ok_body("at", Some("rt2")), ""));
        let out = tauri::async_runtime::block_on(refresh(&Route::direct(), &origin, "rt1")).unwrap();
        assert_eq!(out.rotated_refresh_token.as_deref(), Some("rt2"));
        let req = h.join().unwrap();
        assert!(req.starts_with("POST /api/v1/auth/refresh HTTP/1.1"));
        assert!(req.to_ascii_lowercase().contains("x-client-platform: desktop"));
        assert!(req.contains(r#"{"refreshToken":"rt1"}"#));
    }

    #[test]
    fn exchange_posts_grant() {
        let (origin, h) = serve_once(http_response("200 OK", &ok_body("at", Some("rt")), ""));
        tauri::async_runtime::block_on(exchange_grant(&Route::direct(), &origin, "g1")).unwrap();
        let req = h.join().unwrap();
        assert!(req.starts_with("POST /api/v1/auth/local/exchange HTTP/1.1"));
        assert!(req.contains(r#"{"grant":"g1"}"#));
    }

    #[test]
    fn redirects_are_not_followed() {
        let (origin, _h) = serve_once(http_response("302 Found", b"", "Location: http://127.0.0.1:1/x\r\n"));
        let e = tauri::async_runtime::block_on(refresh(&Route::direct(), &origin, "rt")).unwrap_err();
        assert_eq!(e.kind, ErrorKind::Http);
        assert_eq!(e.status, Some(302));
    }

    #[test]
    fn oversize_body_is_a_network_error() {
        let big = vec![b'x'; MAX_BODY + 10];
        let (origin, _h) = serve_once(http_response("200 OK", &big, ""));
        let e = tauri::async_runtime::block_on(refresh(&Route::direct(), &origin, "rt")).unwrap_err();
        assert_eq!(e.kind, ErrorKind::Network);
        assert_eq!(e.message, "response too large");
    }
}
