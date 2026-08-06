//! Credential store: refresh tokens and the proxy password live in the
//! operating system's secret storage (Keychain, Credential Manager, Secret
//! Service), never in files and never readable by page scripts.

use crate::error::ShellError;
use crate::server::Server;

pub const PROXY_PASSWORD_ACCOUNT: &str = "proxy-password";

/// Credential-store account holding the refresh token of a server.
pub fn refresh_account(server: &Server) -> String {
    format!("refresh-token:{}", server.credential_key())
}

/// A minimal key/value secret store. `get` of a missing entry is `Ok(None)`
/// and `delete` of a missing entry is `Ok(())`.
pub trait SecretStore: Send + Sync {
    fn get(&self, account: &str) -> Result<Option<String>, ShellError>;
    fn set(&self, account: &str, secret: &str) -> Result<(), ShellError>;
    fn delete(&self, account: &str) -> Result<(), ShellError>;
}

/// The operating system credential store, keyed by service name (the app
/// identifier, so dev, installed and portable copies are isolated).
pub struct KeyringStore {
    service: String,
}

impl KeyringStore {
    pub fn new(service: impl Into<String>) -> Self {
        Self {
            service: service.into(),
        }
    }

    fn entry(&self, account: &str) -> Result<keyring::Entry, ShellError> {
        keyring::Entry::new(&self.service, account).map_err(storage_error)
    }
}

fn storage_error(e: keyring::Error) -> ShellError {
    // the message never includes the secret
    ShellError::storage(format!("credential store error: {e}"))
}

impl SecretStore for KeyringStore {
    fn get(&self, account: &str) -> Result<Option<String>, ShellError> {
        match self.entry(account)?.get_password() {
            Ok(v) => Ok(Some(v)),
            Err(keyring::Error::NoEntry) => Ok(None),
            Err(e) => Err(storage_error(e)),
        }
    }

    fn set(&self, account: &str, secret: &str) -> Result<(), ShellError> {
        self.entry(account)?.set_password(secret).map_err(storage_error)
    }

    fn delete(&self, account: &str) -> Result<(), ShellError> {
        match self.entry(account)?.delete_credential() {
            Ok(()) | Err(keyring::Error::NoEntry) => Ok(()),
            Err(e) => Err(storage_error(e)),
        }
    }
}

/// In-memory store used by tests.
#[cfg(test)]
#[derive(Default)]
pub struct MemoryStore {
    map: std::sync::Mutex<std::collections::HashMap<String, String>>,
    pub fail: std::sync::atomic::AtomicBool,
}

#[cfg(test)]
impl MemoryStore {
    pub fn contents(&self) -> std::collections::HashMap<String, String> {
        self.map.lock().unwrap().clone()
    }

    fn check(&self) -> Result<(), ShellError> {
        if self.fail.load(std::sync::atomic::Ordering::SeqCst) {
            return Err(ShellError::storage("store unavailable"));
        }
        Ok(())
    }
}

#[cfg(test)]
impl SecretStore for MemoryStore {
    fn get(&self, account: &str) -> Result<Option<String>, ShellError> {
        self.check()?;
        Ok(self.map.lock().unwrap().get(account).cloned())
    }

    fn set(&self, account: &str, secret: &str) -> Result<(), ShellError> {
        self.check()?;
        self.map.lock().unwrap().insert(account.into(), secret.into());
        Ok(())
    }

    fn delete(&self, account: &str) -> Result<(), ShellError> {
        self.check()?;
        self.map.lock().unwrap().remove(account);
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn account_names() {
        assert_eq!(refresh_account(&Server::local()), "refresh-token:local");
        assert_eq!(
            refresh_account(&Server::remote("https://chat.example.com")),
            "refresh-token:https://chat.example.com"
        );
        assert_eq!(PROXY_PASSWORD_ACCOUNT, "proxy-password");
    }

    #[test]
    fn memory_store_semantics() {
        let s = MemoryStore::default();
        assert_eq!(s.get("a").unwrap(), None);
        s.delete("a").unwrap();
        s.set("a", "x").unwrap();
        assert_eq!(s.get("a").unwrap().as_deref(), Some("x"));
        s.delete("a").unwrap();
        assert_eq!(s.get("a").unwrap(), None);
    }
}
