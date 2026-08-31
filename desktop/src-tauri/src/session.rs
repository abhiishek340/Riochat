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
