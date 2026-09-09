//! Proxy endpoints: parsing, canonical form and credential handling.

use percent_encoding::{percent_decode_str, utf8_percent_encode, AsciiSet, NON_ALPHANUMERIC};
use std::fmt;
use url::{Host, Url};

/// Everything except RFC 3986 unreserved characters is escaped, so a password
/// such as `p@ss:w/rd` survives inside a URL's userinfo.
const USERINFO: &AsciiSet = &NON_ALPHANUMERIC.remove(b'-').remove(b'.').remove(b'_').remove(b'~');

#[derive(Debug, Clone, Copy, PartialEq, Eq, serde::Serialize, serde::Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Scheme {
    Http,
    Socks5,
}

impl Scheme {
    pub fn as_str(self) -> &'static str {
        match self {
            Scheme::Http => "http",
            Scheme::Socks5 => "socks5",
        }
    }
}

#[derive(Clone, PartialEq, Eq)]
pub struct Credentials {
    pub username: String,
    pub password: String,
}

// The password must never reach a log or a debug dump.
impl fmt::Debug for Credentials {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("Credentials")
            .field("username", &self.username)
            .field("password", &"<redacted>")
            .finish()
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Endpoint {
    pub scheme: Scheme,
    /// Host name or address without brackets.
    pub host: String,
    pub port: u16,
    pub credentials: Option<Credentials>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum AddressError {
    Empty,
    Scheme,
    Credentials,
    Extra,
    Host,
    Port,
}

impl fmt::Display for AddressError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(match self {
            AddressError::Empty => "the proxy address is empty",
            AddressError::Scheme => "only http and socks5 proxies are supported",
            AddressError::Credentials => "the proxy address must not contain credentials",
            AddressError::Extra => "the proxy address must not contain a path, query or fragment",
            AddressError::Host => "the proxy address has no valid host",
            AddressError::Port => "the proxy address has an invalid port",
        })
    }
}

impl Endpoint {
    fn host_for_url(&self) -> String {
        if self.host.contains(':') {
            format!("[{}]", self.host)
        } else {
            self.host.clone()
        }
    }

    /// `scheme://host:port` without credentials.
    pub fn address(&self) -> String {
        format!("{}://{}:{}", self.scheme.as_str(), self.host_for_url(), self.port)
    }

    /// `scheme://[user:pass@]host:port/` with the credentials percent-encoded,
    /// the form placed in child process environment variables.
    pub fn url_string(&self) -> String {
        let auth = match &self.credentials {
            Some(c) => format!(
                "{}:{}@",
                utf8_percent_encode(&c.username, USERINFO),
                utf8_percent_encode(&c.password, USERINFO)
            ),
            None => String::new(),
        };
        format!(
            "{}://{}{}:{}/",
            self.scheme.as_str(),
            auth,
            self.host_for_url(),
            self.port
        )
    }

    /// The same endpoint as a parsed URL (credentials included).
    pub fn to_url(&self) -> Option<Url> {
        Url::parse(&self.url_string()).ok()
    }

    pub fn with_credentials(mut self, credentials: Option<Credentials>) -> Self {
        self.credentials = credentials;
        self
    }
}

/// Canonicalizes `host:port`, `http://host:port` or `socks5://host:port`.
/// A bare host gets `http`; the default http port 80 is filled in; SOCKS needs
/// an explicit port. Credentials are rejected.
pub fn canonicalize(input: &str) -> Result<Endpoint, AddressError> {
    parse(input, false)
}

/// Like [`canonicalize`] but accepts `user:pass@` (used for environment
/// variables, which commonly carry proxy credentials).
pub fn canonicalize_with_credentials(input: &str) -> Result<Endpoint, AddressError> {
    parse(input, true)
}

fn parse(input: &str, allow_credentials: bool) -> Result<Endpoint, AddressError> {
    let trimmed = input.trim();
    if trimmed.is_empty() {
        return Err(AddressError::Empty);
    }
    let with_scheme = if trimmed.contains("://") {
        trimmed.to_string()
    } else {
        format!("http://{trimmed}")
    };
    let url = Url::parse(&with_scheme).map_err(|_| AddressError::Host)?;
    let scheme = match url.scheme().to_ascii_lowercase().as_str() {
        "http" => Scheme::Http,
        "socks5" => Scheme::Socks5,
        _ => return Err(AddressError::Scheme),
    };
    let credentials = if url.username().is_empty() && url.password().is_none() {
        None
    } else if allow_credentials {
        let user = percent_decode_str(url.username()).decode_utf8_lossy().into_owned();
        let pass = percent_decode_str(url.password().unwrap_or(""))
            .decode_utf8_lossy()
            .into_owned();
        Some(Credentials {
            username: user,
            password: pass,
        })
    } else {
        return Err(AddressError::Credentials);
    };
    if (url.path() != "" && url.path() != "/") || url.query().is_some() || url.fragment().is_some() {
        return Err(AddressError::Extra);
    }
    let host = match url.host() {
        Some(Host::Domain(d)) if !d.is_empty() => d.to_ascii_lowercase(),
        Some(Host::Ipv4(a)) => a.to_string(),
        Some(Host::Ipv6(a)) => a.to_string(),
        _ => return Err(AddressError::Host),
    };
    if host.chars().any(|c| c.is_whitespace() || "/@?#".contains(c)) {
        return Err(AddressError::Host);
    }
    let port = match (url.port(), scheme) {
        (Some(0), _) => return Err(AddressError::Port),
        (Some(p), _) => p,
        (None, Scheme::Http) => 80,
        (None, Scheme::Socks5) => return Err(AddressError::Port),
    };
    Ok(Endpoint {
        scheme,
        host,
        port,
        credentials,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn accepts_the_documented_forms() {
        assert_eq!(
            canonicalize("proxy.corp:3128").unwrap().address(),
            "http://proxy.corp:3128"
        );
        assert_eq!(
            canonicalize("http://proxy.corp:3128").unwrap().address(),
            "http://proxy.corp:3128"
        );
        assert_eq!(
            canonicalize("socks5://10.0.0.2:1080").unwrap().address(),
            "socks5://10.0.0.2:1080"
        );
        assert_eq!(canonicalize("proxy.corp").unwrap().address(), "http://proxy.corp:80");
        assert_eq!(
            canonicalize(" HTTP://Proxy.Corp:8080/ ").unwrap().address(),
            "http://proxy.corp:8080"
        );
        assert_eq!(canonicalize("[::1]:3128").unwrap().address(), "http://[::1]:3128");
    }

    #[test]
    fn rejects_bad_forms() {
        assert_eq!(canonicalize("https://proxy:443"), Err(AddressError::Scheme));
        assert_eq!(canonicalize("http://u:p@proxy:80"), Err(AddressError::Credentials));
        assert_eq!(canonicalize("http://proxy:80/path"), Err(AddressError::Extra));
        assert_eq!(canonicalize("http://proxy:80/?a=1"), Err(AddressError::Extra));
        assert_eq!(canonicalize("http://proxy:80/#x"), Err(AddressError::Extra));
        assert_eq!(canonicalize("socks5://proxy"), Err(AddressError::Port));
        assert_eq!(canonicalize("http://proxy:0"), Err(AddressError::Port));
        assert_eq!(canonicalize(""), Err(AddressError::Empty));
        assert!(canonicalize("http://").is_err());
        assert!(canonicalize("ftp://proxy:21").is_err());
    }

    #[test]
    fn environment_style_credentials_round_trip() {
        let e = canonicalize_with_credentials("http://alice:p%40ss%3Aw%2Frd@proxy.corp:3128").unwrap();
        let c = e.credentials.clone().unwrap();
        assert_eq!(c.username, "alice");
        assert_eq!(c.password, "p@ss:w/rd");
        assert_eq!(e.url_string(), "http://alice:p%40ss%3Aw%2Frd@proxy.corp:3128/");
        assert_eq!(e.address(), "http://proxy.corp:3128");
    }

    #[test]
    fn debug_never_shows_the_password() {
        let e = canonicalize("proxy.corp:1")
            .unwrap()
            .with_credentials(Some(Credentials {
                username: "bob".into(),
                password: "hunter2".into(),
            }));
        let dump = format!("{e:?}");
        assert!(!dump.contains("hunter2"));
        assert!(dump.contains("bob"));
    }
}
