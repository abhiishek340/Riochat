//! Error type returned to content tabs by the session, server and update
//! commands. It serializes as `{ kind, message, status?, errorCode? }`.

use serde::Serialize;
use std::fmt;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum ErrorKind {
    Network,
    Http,
    Storage,
    NoSession,
    InvalidOrigin,
    Sidecar,
    NoServer,
    Tabs,
    Policy,
}

#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ShellError {
    pub kind: ErrorKind,
    pub message: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub status: Option<u16>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error_code: Option<String>,
}

impl ShellError {
    pub fn new(kind: ErrorKind, message: impl Into<String>) -> Self {
        Self {
            kind,
            message: message.into(),
            status: None,
            error_code: None,
        }
    }

    pub fn network(message: impl Into<String>) -> Self {
        Self::new(ErrorKind::Network, message)
    }

    pub fn storage(message: impl Into<String>) -> Self {
        Self::new(ErrorKind::Storage, message)
    }

    pub fn sidecar(message: impl Into<String>) -> Self {
        Self::new(ErrorKind::Sidecar, message)
    }

    pub fn tabs(message: impl Into<String>) -> Self {
        Self::new(ErrorKind::Tabs, message)
    }

    pub fn invalid_origin(message: impl Into<String>) -> Self {
        Self::new(ErrorKind::InvalidOrigin, message)
    }

    pub fn no_server() -> Self {
        Self::new(ErrorKind::NoServer, "this tab is not connected to a server")
    }

    pub fn no_session() -> Self {
        Self::new(ErrorKind::NoSession, "no stored session")
    }

    /// Policy errors always start with `policy: ` (the web client maps that
    /// prefix to its "managed by your organization" wording).
    pub fn policy(message: &str) -> Self {
        Self::new(ErrorKind::Policy, format!("policy: {message}"))
    }

    pub fn http(status: u16, error_code: Option<String>, message: impl Into<String>) -> Self {
        Self {
            kind: ErrorKind::Http,
            message: message.into(),
            status: Some(status),
            error_code,
        }
    }

    pub fn is_unauthorized(&self) -> bool {
        self.kind == ErrorKind::Http && self.status == Some(401)
    }
}

impl fmt::Display for ShellError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.message)
    }
}

impl std::error::Error for ShellError {}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn serializes_with_camel_case_and_snake_kind() {
        let e = ShellError::http(401, Some("auth.invalid_refresh_token".into()), "nope");
        let v = serde_json::to_value(&e).unwrap();
        assert_eq!(v["kind"], "http");
        assert_eq!(v["status"], 401);
        assert_eq!(v["errorCode"], "auth.invalid_refresh_token");
        let v = serde_json::to_value(ShellError::no_session()).unwrap();
        assert_eq!(v["kind"], "no_session");
        assert!(v.get("status").is_none());
        assert!(v.get("errorCode").is_none());
        let v = serde_json::to_value(ShellError::invalid_origin("x")).unwrap();
        assert_eq!(v["kind"], "invalid_origin");
    }

    #[test]
    fn policy_messages_carry_prefix() {
        assert!(ShellError::policy("local mode is disabled by your administrator")
            .message
            .starts_with("policy: "));
    }
}
