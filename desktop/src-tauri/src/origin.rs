//! Server origins: normalization and loopback detection.

use std::net::IpAddr;
use url::{Host, Url};

/// Normalizes a user supplied server address to `scheme://host[:port]`.
///
/// Only absolute http(s) URLs with a host, no credentials, no query, no
/// fragment and an empty or `/` path are accepted. Default ports are dropped.
pub fn normalize_origin(input: &str) -> Option<String> {
    let url = Url::parse(input.trim()).ok()?;
    if url.scheme() != "http" && url.scheme() != "https" {
        return None;
    }
    if !url.username().is_empty() || url.password().is_some() {
        return None;
    }
    if url.query().is_some() || url.fragment().is_some() {
        return None;
    }
    if url.path() != "" && url.path() != "/" {
        return None;
    }
    let host = match url.host()? {
        Host::Domain(d) => d.to_ascii_lowercase(),
        Host::Ipv4(a) => a.to_string(),
        Host::Ipv6(a) => format!("[{a}]"),
    };
    if host.is_empty() {
        return None;
    }
    Some(match url.port() {
        Some(p) => format!("{}://{}:{}", url.scheme(), host, p),
        None => format!("{}://{}", url.scheme(), host),
    })
}

/// True for `localhost` (any case), any 127.x.x.x address and `::1`.
pub fn is_loopback_host(host: &str) -> bool {
    let h = host.trim();
    let h = h.strip_prefix('[').and_then(|x| x.strip_suffix(']')).unwrap_or(h);
    let h = h.strip_suffix('.').unwrap_or(h);
    if h.eq_ignore_ascii_case("localhost") {
        return true;
    }
    match h.parse::<IpAddr>() {
        Ok(ip) => ip.is_loopback(),
        Err(_) => false,
    }
}

/// True when the URL's host is a loopback host.
pub fn is_loopback_url(url: &Url) -> bool {
    match url.host() {
        Some(Host::Domain(d)) => is_loopback_host(d),
        Some(Host::Ipv4(a)) => a.is_loopback(),
        Some(Host::Ipv6(a)) => a.is_loopback(),
        None => false,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn normalizes_case_ports_and_slash() {
        assert_eq!(
            normalize_origin("HTTPS://Chat.Example.com:443/").as_deref(),
            Some("https://chat.example.com")
        );
        assert_eq!(
            normalize_origin(" http://127.0.0.1:8080/ ").as_deref(),
            Some("http://127.0.0.1:8080")
        );
        assert_eq!(normalize_origin("http://host:80").as_deref(), Some("http://host"));
        assert_eq!(
            normalize_origin("https://host:8443").as_deref(),
            Some("https://host:8443")
        );
        assert_eq!(
            normalize_origin("http://[::1]:3000/").as_deref(),
            Some("http://[::1]:3000")
        );
    }

    #[test]
    fn rejects_everything_else() {
        for bad in [
            "chat.example.com",
            "ftp://x",
            "https://u:p@x",
            "https://x/api",
            "https://x/?q=1",
            "https://x/#f",
            "javascript:alert(1)",
            "",
            "   ",
            "https://x//",
            "https://x?",
        ] {
            assert_eq!(normalize_origin(bad), None, "{bad:?} must be rejected");
        }
    }

    #[test]
    fn loopback_detection() {
        for yes in ["localhost", "LOCALHOST", "127.0.0.1", "127.9.9.9", "::1", "[::1]"] {
            assert!(is_loopback_host(yes), "{yes}");
        }
        for no in [
            "192.168.1.1",
            "10.0.0.1",
            "localhost.evil.com",
            "127.0.0.1.nip.io",
            "mylocalhost",
            "",
            "example.com",
        ] {
            assert!(!is_loopback_host(no), "{no}");
        }
        assert!(is_loopback_url(&Url::parse("http://127.0.0.1:1/").unwrap()));
        assert!(is_loopback_url(&Url::parse("http://[::1]:1/").unwrap()));
        assert!(!is_loopback_url(&Url::parse("http://example.com/").unwrap()));
    }
}
