//! Third-party sign-in through the system browser (RFC 8252 loopback
//! redirect). A listener bound to 127.0.0.1 on an ephemeral port serves the
//! provider's redirect, tells the user to return to the app and hands the full
//! callback URL to the shell.

use percent_encoding::{utf8_percent_encode, NON_ALPHANUMERIC};
use std::io::{Read, Write};
use std::net::{Ipv4Addr, SocketAddr, TcpListener, TcpStream};
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Arc;
use std::time::{Duration, Instant};

pub const CALLBACK_PATH: &str = "/oauth/callback";
pub const LISTEN_FOR: Duration = Duration::from_secs(600);
pub const HEAD_TIMEOUT: Duration = Duration::from_secs(5);
pub const HEAD_LIMIT: usize = 16 * 1024;
const POLL: Duration = Duration::from_millis(50);

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Request {
    /// A well-formed GET for the callback path; carries the request target.
    Callback(String),
    BadRequest,
    NotFound,
}

/// Classifies the head of an HTTP request.
pub fn classify(head: &[u8]) -> Request {
    let Ok(text) = std::str::from_utf8(head) else {
        return Request::BadRequest;
    };
    let line = text.lines().next().unwrap_or("");
    let mut parts = line.split(' ');
    let (Some(method), Some(target), Some(version), None) = (parts.next(), parts.next(), parts.next(), parts.next())
    else {
        return Request::BadRequest;
    };
    if method != "GET" || !version.starts_with("HTTP/") {
        return Request::BadRequest;
    }
    if !target.starts_with('/') || target.contains("..") || target.chars().any(|c| c.is_control()) {
        return Request::BadRequest;
    }
    if !target.starts_with(CALLBACK_PATH) {
        return Request::NotFound;
    }
    Request::Callback(target.to_string())
}

fn svg_mark(ink: &str) -> String {
    let svg = format!(
        "<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 100 100'>\
         <circle cx='50' cy='48' r='21.5' fill='none' stroke='{ink}' stroke-width='11'/>\
         <polygon points='32,62 27,80 47,71' fill='{ink}'/>\
         <circle cx='50' cy='48' r='7' fill='{ink}'/></svg>"
    );
    format!("data:image/svg+xml,{}", utf8_percent_encode(&svg, NON_ALPHANUMERIC))
}

struct Content {
    status_line: &'static str,
    en_title: &'static str,
    en_text: &'static str,
}

fn content(status: u16) -> Content {
    match status {
        200 => Content {
            status_line: "200 OK",
            en_title: "Signed in successfully",
            en_text: "This tab can be closed now. Head back to Riochat.",
        },
        400 => Content {
            status_line: "400 Bad Request",
            en_title: "Sign-in did not finish",
            en_text: "The provider sent back a response we could not accept. Go back to Riochat and try again.",
        },
        _ => Content {
            status_line: "404 Not Found",
            en_title: "Page not found",
            en_text: "Nothing at this address belongs to the sign-in process. Go back to Riochat.",
        },
    }
}

/// The self-contained page for a status (unknown statuses render the 404
/// content).
pub fn page(status: u16) -> String {
    let c = content(status);
    let light = svg_mark("#3d3929");
    let dark = svg_mark("#f8f8f6");
    format!(
        "<!doctype html>\n<html lang=\"en\"><head><meta charset=\"utf-8\">\
<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\
<meta name=\"robots\" content=\"noindex\"><title>Riochat</title>\
<style>\
:root{{color-scheme:light dark;--bg:#f8f8f6;--fg:#3d3929;--muted:#6b6757}}\
@media (prefers-color-scheme:dark){{:root{{--bg:#262624;--fg:#f8f8f6;--muted:#b8b5a8}}}}\
*{{box-sizing:border-box}}\
body{{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;\
background:var(--bg);color:var(--fg);font:16px/1.5 system-ui,-apple-system,\"Segoe UI\",sans-serif}}\
main{{max-width:28rem;padding:2rem;text-align:center}}\
img{{width:56px;height:56px;margin-bottom:1rem}}\
.dark{{display:none}}\
@media (prefers-color-scheme:dark){{.light{{display:none}}.dark{{display:inline-block}}}}\
h1{{font-size:1.4rem;margin:0 0 .5rem}}p{{margin:0;color:var(--muted)}}\
</style></head><body><main>\
<img class=\"light\" alt=\"\" src=\"{light}\"><img class=\"dark\" alt=\"\" src=\"{dark}\">\
<h1>{et}</h1>\
<p>{ex}</p>\
</main><script>\
(function(){{try{{history.replaceState(null,\"\",location.pathname)}}catch(e){{}}}})();\
</script></body></html>\n",
        et = c.en_title,
        ex = c.en_text,
    )
}

/// The complete HTTP response for a status.
pub fn response(status: u16) -> Vec<u8> {
    let body = page(status);
    let head = format!(
        "HTTP/1.1 {}\r\nContent-Type: text/html; charset=utf-8\r\nContent-Length: {}\r\n\
Connection: close\r\nCache-Control: no-store\r\n\
Content-Security-Policy: default-src 'none'; img-src data:; style-src 'unsafe-inline'; script-src 'unsafe-inline'\r\n\
X-Content-Type-Options: nosniff\r\nReferrer-Policy: no-referrer\r\n\r\n",
        content(status).status_line,
        body.len()
    );
    let mut out = head.into_bytes();
    out.extend_from_slice(body.as_bytes());
    out
}

/// Reads a request head: up to [`HEAD_LIMIT`] bytes within [`HEAD_TIMEOUT`].
/// `None` means too large, too slow or closed early.
fn read_head(stream: &mut TcpStream) -> Option<Vec<u8>> {
    let deadline = Instant::now() + HEAD_TIMEOUT;
    let mut buf: Vec<u8> = Vec::new();
    let mut chunk = [0u8; 2048];
    loop {
        let left = deadline.checked_duration_since(Instant::now())?;
        stream.set_read_timeout(Some(left.max(Duration::from_millis(1)))).ok()?;
        match stream.read(&mut chunk) {
            Ok(0) => return None,
            Ok(n) => {
                buf.extend_from_slice(&chunk[..n]);
                if buf.windows(4).any(|w| w == b"\r\n\r\n") || buf.windows(2).any(|w| w == b"\n\n") {
                    return Some(buf);
                }
                if buf.len() > HEAD_LIMIT {
                    return None;
                }
            }
            Err(_) => return None,
        }
    }
}

fn handle(stream: &mut TcpStream) -> Request {
    let result = match read_head(stream) {
        Some(head) => classify(&head),
        None => Request::BadRequest,
    };
    let status = match result {
        Request::Callback(_) => 200,
        Request::BadRequest => 400,
        Request::NotFound => 404,
    };
    let _ = stream.write_all(&response(status));
    let _ = stream.flush();
    let _ = stream.shutdown(std::net::Shutdown::Write);
    drain(stream);
    result
}

/// Reads and discards what the client is still sending (briefly), so closing
/// the socket does not reset the connection before the page is delivered.
fn drain(stream: &mut TcpStream) {
    let _ = stream.set_read_timeout(Some(Duration::from_millis(200)));
    let mut sink = [0u8; 4096];
    let mut total = 0usize;
    while total < 256 * 1024 {
        match stream.read(&mut sink) {
            Ok(0) | Err(_) => break,
            Ok(n) => total += n,
        }
    }
}

/// Serves requests until a valid callback arrives, `is_current` turns false
/// or the deadline passes. Invalid requests get their error page and the
/// listener keeps waiting for the real redirect.
pub fn serve<C, F>(listener: TcpListener, is_current: C, deadline: Instant, on_callback: F)
where
    C: Fn() -> bool,
    F: FnOnce(String),
{
    let port = match listener.local_addr() {
        Ok(a) => a.port(),
        Err(_) => return,
    };
    if listener.set_nonblocking(true).is_err() {
        return;
    }
    loop {
        if !is_current() || Instant::now() >= deadline {
            return;
        }
        match listener.accept() {
            Ok((mut stream, _)) => {
                let _ = stream.set_nonblocking(false);
                if let Request::Callback(target) = handle(&mut stream) {
                    if is_current() {
                        on_callback(format!("http://127.0.0.1:{port}{target}"));
                    }
                    return;
                }
            }
            Err(e) if e.kind() == std::io::ErrorKind::WouldBlock => std::thread::sleep(POLL),
            Err(_) => std::thread::sleep(POLL),
        }
    }
}
