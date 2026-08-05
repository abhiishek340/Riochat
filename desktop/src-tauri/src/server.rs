//! A server a tab can be bound to.

use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum ServerMode {
    Local,
    Remote,
}

/// The persisted/bound identity of a server. For the local server `origin`
/// is empty because the port changes on every launch.
#[derive(Debug, Clone, PartialEq, Eq, Hash, Serialize, Deserialize)]
pub struct Server {
    pub mode: ServerMode,
    #[serde(default)]
    pub origin: String,
}

impl Server {
    pub fn local() -> Self {
        Self {
            mode: ServerMode::Local,
            origin: String::new(),
        }
    }

    pub fn remote(origin: impl Into<String>) -> Self {
        Self {
            mode: ServerMode::Remote,
            origin: origin.into(),
        }
    }

    pub fn is_local(&self) -> bool {
        self.mode == ServerMode::Local
    }

    /// Short label used by the strip when a page has no title yet.
    pub fn label(&self) -> String {
        match self.mode {
            ServerMode::Local => "Local".to_string(),
            ServerMode::Remote => self
                .origin
                .split_once("://")
                .map(|(_, rest)| rest.to_string())
                .unwrap_or_else(|| self.origin.clone()),
        }
    }

    /// Credential-store key part: `local` or the normalized origin.
    pub fn credential_key(&self) -> String {
        match self.mode {
            ServerMode::Local => "local".to_string(),
            ServerMode::Remote => self.origin.clone(),
        }
    }
}

/// What `get_server`, `set_remote_server` and `set_local_server` return: the
/// mode plus the live origin.
#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
pub struct ServerInfo {
    pub mode: ServerMode,
    pub origin: String,
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn wire_shape() {
        let v = serde_json::to_value(Server::remote("https://a.b")).unwrap();
        assert_eq!(v["mode"], "remote");
        assert_eq!(v["origin"], "https://a.b");
        let l: Server = serde_json::from_str(r#"{"mode":"local"}"#).unwrap();
        assert_eq!(l, Server::local());
    }

    #[test]
    fn keys_and_labels() {
        assert_eq!(Server::local().credential_key(), "local");
        assert_eq!(Server::remote("https://x.y:8").credential_key(), "https://x.y:8");
        assert_eq!(Server::remote("https://x.y:8").label(), "x.y:8");
    }
}
