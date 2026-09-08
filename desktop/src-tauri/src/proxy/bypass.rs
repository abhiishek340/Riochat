//! Bypass lists: normalization, validation and matching.
//!
//! Input may be Windows style (`;`), macOS style (`,`) or NO_PROXY style.

use std::net::{IpAddr, Ipv4Addr, Ipv6Addr};

/// Entries every normalized list ends up containing.
pub const LOOPBACK_ENTRIES: [&str; 3] = ["localhost", "127.0.0.0/8", "::1"];

fn split_entries(input: &str) -> impl Iterator<Item = &str> {
    input
        .split(|c: char| c == ',' || c == ';' || c.is_whitespace())
        .filter(|s| !s.is_empty())
}

fn is_special_token(entry: &str) -> bool {
    entry.starts_with('<') && entry.ends_with('>')
}

fn valid_name(s: &str) -> bool {
    !s.is_empty() && s.chars().all(|c| c.is_ascii_alphanumeric() || c == '-' || c == '.')
}

fn parse_cidr(entry: &str) -> Option<String> {
    let (addr, prefix) = entry.split_once('/')?;
    let prefix: u8 = prefix.parse().ok()?;
    if let Ok(v6) = addr.parse::<Ipv6Addr>() {
        return (prefix <= 128).then(|| format!("{v6}/{prefix}"));
    }
    // IPv4, possibly abbreviated (`169.254/16`)
    let parts: Vec<&str> = addr.split('.').collect();
    if parts.is_empty() || parts.len() > 4 {
        return None;
    }
    let mut octets = [0u8; 4];
    for (i, p) in parts.iter().enumerate() {
        if p.is_empty() || !p.chars().all(|c| c.is_ascii_digit()) {
            return None;
        }
        octets[i] = p.parse().ok()?;
    }
    (prefix <= 32).then(|| format!("{}/{}", Ipv4Addr::from(octets), prefix))
}

/// `10.*`, `172.16.*`, `10.1.2.*`, `10.*.*.*` -> CIDR; `10.*.1.*` is invalid.
fn parse_wildcard_range(entry: &str) -> Option<Option<String>> {
    if !entry.contains('*') {
        return None;
    }
    let parts: Vec<&str> = entry.split('.').collect();
    if parts.len() > 4 || parts[0] == "*" || parts[0].is_empty() {
        return None;
    }
    if !parts[0].chars().all(|c| c.is_ascii_digit()) {
        return None;
    }
    let mut octets = [0u8; 4];
    let mut numeric = 0usize;
    let mut seen_wildcard = false;
    for (i, p) in parts.iter().enumerate() {
        if *p == "*" {
            seen_wildcard = true;
        } else if p.chars().all(|c| c.is_ascii_digit()) && !p.is_empty() {
            if seen_wildcard {
                return Some(None); // numeric after a wildcard
            }
            octets[i] = match p.parse() {
                Ok(v) => v,
                Err(_) => return Some(None),
            };
            numeric += 1;
        } else {
            return None; // not an address range; let the name rules decide
        }
    }
    if numeric == 0 || numeric > 3 || !seen_wildcard {
        return Some(None);
    }
    Some(Some(format!("{}/{}", Ipv4Addr::from(octets), numeric * 8)))
}

/// Normalizes one already lower-cased, prefix-stripped entry.
fn classify(entry: &str) -> Option<String> {
    if entry == "*" {
        return Some("*".to_string());
    }
    if entry.contains('/') {
        return parse_cidr(entry);
    }
    if let Some(inner) = entry.strip_prefix('[') {
        // bracketed IPv6, optionally with a port
        let (addr, rest) = inner.split_once(']')?;
        let ip: Ipv6Addr = addr.parse().ok()?;
        return match rest {
            "" => Some(ip.to_string()),
            r => {
                let port = r.strip_prefix(':')?;
                port.parse::<u16>().ok()?;
                Some(format!("[{ip}]:{port}"))
            }
        };
    }
    if let Ok(ip) = entry.parse::<IpAddr>() {
        return Some(ip.to_string());
    }
    if let Some(range) = parse_wildcard_range(entry) {
        return range;
    }
    if entry.contains('*') {
        // `*.corp` and `*corp` -> `.corp`
        let rest = entry.strip_prefix('*')?;
        let rest = rest.strip_prefix('.').unwrap_or(rest);
        return valid_name(rest).then(|| format!(".{rest}"));
    }
    // host name with an optional numeric port
    let (name, port) = match entry.rsplit_once(':') {
        Some((n, p)) => (n, Some(p)),
        None => (entry, None),
    };
    if !valid_name(name) {
        return None;
    }
    match port {
        Some(p) => {
            if p.is_empty() || !p.chars().all(|c| c.is_ascii_digit()) || p.parse::<u16>().is_err() {
                return None;
            }
            Some(format!("{name}:{p}"))
        }
        None => Some(name.to_string()),
    }
}

fn prepare(raw: &str) -> String {
    let mut e = raw.trim().to_ascii_lowercase();
    for prefix in ["http://", "https://"] {
        if let Some(rest) = e.strip_prefix(prefix) {
            e = rest.to_string();
            break;
        }
    }
    while e.ends_with('/') && e.len() > 1 {
        e.pop();
    }
    e
}

/// Normalizes a bypass list to a comma-joined list with the loopback entries
/// always present.
pub fn normalize(input: &str) -> String {
    let mut out: Vec<String> = Vec::new();
    for raw in split_entries(input) {
        let e = prepare(raw);
        if e.is_empty() || is_special_token(&e) || e.contains('@') {
            continue;
        }
        if let Some(n) = classify(&e) {
            if !out.contains(&n) {
                out.push(n);
            }
        }
    }
    for l in LOOPBACK_ENTRIES {
        if !out.iter().any(|x| x == l) {
            out.push(l.to_string());
        }
    }
    out.join(",")
}

/// Returns the first entry of `input` that is neither a recognised special
/// token nor a valid host, address or range.
pub fn first_invalid(input: &str) -> Option<String> {
    for raw in split_entries(input) {
        let e = prepare(raw);
        if e == "<local>" || e == "<-loopback>" {
            continue;
        }
        if e.is_empty() || is_special_token(&e) || e.contains('@') || classify(&e).is_none() {
            return Some(raw.to_string());
        }
    }
    None
}

/// The list with the loopback entries removed, joined with `;` for the
/// WebView2 `--proxy-bypass-list` switch.
pub fn without_loopback_semicolon(normalized: &str) -> String {
    normalized
        .split(',')
        .filter(|e| !e.is_empty() && !LOOPBACK_ENTRIES.contains(e))
        .collect::<Vec<_>>()
        .join(";")
}

/// A parsed, normalized list that can answer "is this destination bypassed".
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct BypassList {
    entries: Vec<String>,
}

impl BypassList {
    pub fn parse(normalized: &str) -> Self {
        Self {
            entries: split_entries(normalized).map(|s| s.to_string()).collect(),
        }
    }

    pub fn matches(&self, host: &str, port: Option<u16>) -> bool {
        let host = host
            .trim()
            .trim_start_matches('[')
            .trim_end_matches(']')
            .to_ascii_lowercase();
        let ip = host.parse::<IpAddr>().ok();
        self.entries.iter().any(|entry| entry_matches(entry, &host, ip, port))
    }
}

fn entry_matches(entry: &str, host: &str, ip: Option<IpAddr>, port: Option<u16>) -> bool {
    if entry == "*" {
        return true;
    }
    if let Some((addr, prefix)) = entry.split_once('/') {
        let (Some(ip), Ok(prefix)) = (ip, prefix.parse::<u32>()) else {
            return false;
        };
        let Ok(net) = addr.parse::<IpAddr>() else {
            return false;
        };
        return cidr_contains(net, prefix, ip);
    }
    if let Some(suffix) = entry.strip_prefix('.') {
        return host == suffix || host.ends_with(entry);
    }
    // `[v6]:port`
    if let Some(inner) = entry.strip_prefix('[') {
        if let Some((addr, rest)) = inner.split_once("]:") {
            return host == addr && port.map(|p| p.to_string()) == Some(rest.to_string());
        }
    }
    if let Ok(entry_ip) = entry.parse::<IpAddr>() {
        return ip == Some(entry_ip);
    }
    let (name, entry_port) = match entry.rsplit_once(':') {
        Some((n, p)) => (n, p.parse::<u16>().ok()),
        None => (entry, None),
    };
    if name != host {
        return false;
    }
    match entry_port {
        Some(p) => port == Some(p),
        None => true,
    }
}

fn cidr_contains(net: IpAddr, prefix: u32, ip: IpAddr) -> bool {
    match (net, ip) {
        (IpAddr::V4(n), IpAddr::V4(a)) if prefix <= 32 => {
            let mask = if prefix == 0 { 0 } else { u32::MAX << (32 - prefix) };
            u32::from(n) & mask == u32::from(a) & mask
        }
        (IpAddr::V6(n), IpAddr::V6(a)) if prefix <= 128 => {
            let mask = if prefix == 0 { 0 } else { u128::MAX << (128 - prefix) };
            u128::from(n) & mask == u128::from(a) & mask
        }
        _ => false,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn documented_example() {
        assert_eq!(
            normalize("localhost;127.*;10.*;172.16.*;172.31.*;192.168.*;*.local;<local>"),
            "localhost,127.0.0.0/8,10.0.0.0/8,172.16.0.0/16,172.31.0.0/16,192.168.0.0/16,.local,::1"
        );
    }

    #[test]
    fn empty_input_gives_loopback_only() {
        assert_eq!(normalize(""), "localhost,127.0.0.0/8,::1");
        assert_eq!(normalize("  ;, "), "localhost,127.0.0.0/8,::1");
    }

    #[test]
    fn wildcard_ranges() {
        assert_eq!(normalize("10.1.2.*"), "10.1.2.0/24,localhost,127.0.0.0/8,::1");
        assert_eq!(normalize("10.*.*.*"), "10.0.0.0/8,localhost,127.0.0.0/8,::1");
        assert_eq!(normalize("10.*.1.*"), "localhost,127.0.0.0/8,::1");
    }

    #[test]
    fn cidr_rules() {
        assert_eq!(normalize("169.254/16"), "169.254.0.0/16,localhost,127.0.0.0/8,::1");
        assert_eq!(normalize("10.0.0.0/33"), "localhost,127.0.0.0/8,::1");
        assert_eq!(normalize("fd00::/8"), "fd00::/8,localhost,127.0.0.0/8,::1");
        assert_eq!(normalize("fd00::/129"), "localhost,127.0.0.0/8,::1");
    }

    #[test]
    fn names_ports_prefixes_dedupe() {
        assert_eq!(
            normalize("HTTP://NAS:5000/, nas:5000 https://Wiki.Corp/ *corp"),
            "nas:5000,wiki.corp,.corp,localhost,127.0.0.0/8,::1"
        );
        assert_eq!(normalize("nas:abc"), "localhost,127.0.0.0/8,::1");
        assert_eq!(normalize("a_b"), "localhost,127.0.0.0/8,::1");
        assert_eq!(normalize("user@host"), "localhost,127.0.0.0/8,::1");
        assert_eq!(normalize("*"), "*,localhost,127.0.0.0/8,::1");
        assert_eq!(normalize("1.2.3.4"), "1.2.3.4,localhost,127.0.0.0/8,::1");
    }

    #[test]
    fn validation_reports_first_bad_entry() {
        assert_eq!(first_invalid("a.corp, <local>, <-loopback>, 10.*"), None);
        assert_eq!(first_invalid("a.corp;bad_name;c"), Some("bad_name".to_string()));
        assert_eq!(first_invalid("<weird>"), Some("<weird>".to_string()));
        assert_eq!(first_invalid("10.*.1.*"), Some("10.*.1.*".to_string()));
    }

    #[test]
    fn webview_list_drops_loopback() {
        assert_eq!(
            without_loopback_semicolon("10.0.0.0/8,.corp,localhost,127.0.0.0/8,::1"),
            "10.0.0.0/8;.corp"
        );
    }

    #[test]
    fn matching() {
        let l = BypassList::parse(&normalize("10.*;.corp;nas:5000;wiki.example"));
        assert!(l.matches("10.2.3.4", Some(80)));
        assert!(!l.matches("11.2.3.4", None));
        assert!(l.matches("a.corp", None));
        assert!(l.matches("corp", None));
        assert!(!l.matches("acorp", None));
        assert!(l.matches("nas", Some(5000)));
        assert!(!l.matches("nas", Some(80)));
        assert!(l.matches("wiki.example", Some(443)));
        assert!(l.matches("localhost", None));
        assert!(l.matches("127.0.0.1", None));
        assert!(l.matches("::1", None));
        assert!(l.matches("[::1]", None));
        assert!(!l.matches("example.com", None));
        assert!(BypassList::parse("*").matches("anything.test", None));
    }
}
