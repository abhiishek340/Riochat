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
