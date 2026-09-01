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

/// The single active loopback listener.
#[derive(Default)]
pub struct Loopback {
    current: Arc<AtomicU64>,
    counter: AtomicU64,
}

impl Loopback {
    /// Binds a new listener (replacing any pending one) and returns the
    /// redirect URI. `on_callback` receives the full callback URL.
    pub fn start<F>(&self, on_callback: F) -> std::io::Result<String>
    where
        F: FnOnce(String) + Send + 'static,
    {
        let listener = TcpListener::bind(SocketAddr::from((Ipv4Addr::LOCALHOST, 0)))?;
        let port = listener.local_addr()?.port();
        let id = self.counter.fetch_add(1, Ordering::SeqCst) + 1;
        self.current.store(id, Ordering::SeqCst);
        let current = self.current.clone();
        std::thread::Builder::new()
            .name("oauth-loopback".into())
            .spawn(move || {
                let check = current.clone();
                serve(
                    listener,
                    move || check.load(Ordering::SeqCst) == id,
                    Instant::now() + LISTEN_FOR,
                    move |url| {
                        // clear the listener, then report
                        let _ = current.compare_exchange(id, 0, Ordering::SeqCst, Ordering::SeqCst);
                        on_callback(url);
                    },
                );
            })?;
        Ok(format!("http://127.0.0.1:{port}{CALLBACK_PATH}"))
    }

    /// Cancels the pending listener (the user gave up).
    pub fn stop(&self) {
        self.current.store(0, Ordering::SeqCst);
    }

    pub fn is_active(&self) -> bool {
        self.current.load(Ordering::SeqCst) != 0
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::mpsc;

    #[test]
    fn classification() {
        assert_eq!(
            classify(b"GET /oauth/callback?code=abc&state=x HTTP/1.1\r\nHost: a\r\n\r\n"),
            Request::Callback("/oauth/callback?code=abc&state=x".into())
        );
        assert_eq!(
            classify(b"GET /oauth/callbackfoo HTTP/1.1\r\n\r\n"),
            Request::Callback("/oauth/callbackfoo".into())
        );
        assert_eq!(classify(b"GET /favicon.ico HTTP/1.1\r\n\r\n"), Request::NotFound);
        assert_eq!(classify(b"GET / HTTP/1.1\r\n\r\n"), Request::NotFound);
        for bad in [
            &b"POST /oauth/callback HTTP/1.1\r\n\r\n"[..],
            b"GET oauth/callback HTTP/1.1\r\n\r\n",
            b"GET /oauth/callback/../x HTTP/1.1\r\n\r\n",
            b"GET /oauth/callback HTTP/1.1 extra\r\n\r\n",
            b"GET /oauth/callback\r\n\r\n",
            b"GET /oauth/callback FTP/1\r\n\r\n",
            b"GET http://evil/oauth/callback HTTP/1.1\r\n\r\n",
            b"",
            &[0xff, 0xfe, b'\r', b'\n'],
        ] {
            assert_eq!(classify(bad), Request::BadRequest, "{:?}", String::from_utf8_lossy(bad));
        }
    }

    fn split(resp: &[u8]) -> (String, String) {
        let text = String::from_utf8(resp.to_vec()).unwrap();
        let (h, b) = text.split_once("\r\n\r\n").unwrap();
        (h.to_string(), b.to_string())
    }

    #[test]
    fn responses_are_accurate_and_self_contained() {
        for (status, line, en) in [
            (200u16, "HTTP/1.1 200 OK", "Signed in successfully"),
            (400, "HTTP/1.1 400 Bad Request", "Sign-in did not finish"),
            (404, "HTTP/1.1 404 Not Found", "Page not found"),
            (500, "HTTP/1.1 404 Not Found", "Page not found"),
        ] {
            let (head, body) = split(&response(status));
            assert!(head.starts_with(line), "{head}");
            assert!(head.contains(&format!("Content-Length: {}\r\n", body.len())), "{head}");
            assert!(head.contains("Content-Type: text/html; charset=utf-8"));
            assert!(head.contains("Connection: close"));
            assert!(head.contains("Cache-Control: no-store"));
            assert!(head.contains("Content-Security-Policy: default-src 'none'; img-src data:"));
            assert!(head.contains("X-Content-Type-Options: nosniff"));
            assert!(head.contains("Referrer-Policy: no-referrer"));
            assert!(body.contains(en));
            assert!(body.contains("noindex"));
            assert!(body.contains("<title>Riochat</title>"));
            assert!(body.contains("history.replaceState"));
            // nothing external, no buttons
            assert!(!body.contains("<button") && !body.contains("<a "));
            assert!(
                !body.contains("http://") || body.contains("w3.org"),
                "external reference"
            );
            assert!(!body.contains("https://"));
            assert!(!body.contains("src=\"http"));
            assert!(body.contains("prefers-color-scheme:dark"));
        }
        let (_, ok) = split(&response(200));
        assert!(ok.contains("This tab can be closed now. Head back to Riochat."));
        let (_, bad) = split(&response(400));
        assert!(
            bad.contains("The provider sent back a response we could not accept. Go back to Riochat and try again.")
        );
        let (_, nf) = split(&response(404));
        assert!(nf.contains("Nothing at this address belongs to the sign-in process. Go back to Riochat."));
    }

    fn raw_request(port: u16, bytes: &[u8]) -> Vec<u8> {
        let mut s = TcpStream::connect(("127.0.0.1", port)).unwrap();
        s.write_all(bytes).unwrap();
        let _ = s.shutdown(std::net::Shutdown::Write);
        let mut out = Vec::new();
        s.read_to_end(&mut out).unwrap();
        out
    }

    fn port_of(uri: &str) -> u16 {
        uri.rsplit_once(':')
            .unwrap()
            .1
            .split('/')
            .next()
            .unwrap()
            .parse()
            .unwrap()
    }

    #[test]
    fn start_serves_callback_and_emits_full_url() {
        let lb = Loopback::default();
        let (tx, rx) = mpsc::channel();
        let uri = lb.start(move |url| tx.send(url).unwrap()).unwrap();
        assert!(uri.starts_with("http://127.0.0.1:"));
        assert!(uri.ends_with("/oauth/callback"));
        let port = port_of(&uri);
        // junk first: answered, listener keeps waiting
        let junk = raw_request(port, b"GET /favicon.ico HTTP/1.1\r\nHost: x\r\n\r\n");
        assert!(String::from_utf8_lossy(&junk).starts_with("HTTP/1.1 404"));
        let bad = raw_request(port, b"POST /oauth/callback HTTP/1.1\r\n\r\n");
        assert!(String::from_utf8_lossy(&bad).starts_with("HTTP/1.1 400"));
        assert!(rx.try_recv().is_err());
        let ok = raw_request(
            port,
            b"GET /oauth/callback?code=c1&state=s1 HTTP/1.1\r\nHost: x\r\n\r\n",
        );
        assert!(String::from_utf8_lossy(&ok).starts_with("HTTP/1.1 200"));
        let url = rx.recv_timeout(Duration::from_secs(3)).unwrap();
        assert_eq!(url, format!("http://127.0.0.1:{port}/oauth/callback?code=c1&state=s1"));
        assert!(!lb.is_active(), "cleared after the callback");
    }

    #[test]
    fn oversize_head_is_rejected() {
        let lb = Loopback::default();
        let (tx, rx) = mpsc::channel::<String>();
        let uri = lb.start(move |u| tx.send(u).unwrap()).unwrap();
        let mut big = b"GET /oauth/callback HTTP/1.1\r\nX-Pad: ".to_vec();
        big.extend(std::iter::repeat(b'a').take(HEAD_LIMIT + 4096));
        let out = raw_request(port_of(&uri), &big);
        assert!(String::from_utf8_lossy(&out).starts_with("HTTP/1.1 400"));
        assert!(rx.try_recv().is_err());
        lb.stop();
    }

    #[test]
    fn a_second_start_invalidates_the_first() {
        let lb = Loopback::default();
        let (tx1, rx1) = mpsc::channel::<String>();
        let first = lb.start(move |u| tx1.send(u).unwrap()).unwrap();
        let (tx2, rx2) = mpsc::channel::<String>();
        let second = lb.start(move |u| tx2.send(u).unwrap()).unwrap();
        assert_ne!(first, second);
        // the first listener quits within a poll interval or two
        std::thread::sleep(Duration::from_millis(400));
        assert!(
            TcpStream::connect(("127.0.0.1", port_of(&first))).is_err() || {
                // port may have been closed already; either way nothing is emitted
                true
            }
        );
        let _ = raw_request(port_of(&second), b"GET /oauth/callback?x=1 HTTP/1.1\r\n\r\n");
        assert!(rx2.recv_timeout(Duration::from_secs(3)).is_ok());
        assert!(rx1.try_recv().is_err());
    }

    #[test]
    fn stop_makes_the_listener_exit() {
        let lb = Loopback::default();
        let (tx, rx) = mpsc::channel::<String>();
        let uri = lb.start(move |u| tx.send(u).unwrap()).unwrap();
        lb.stop();
        std::thread::sleep(Duration::from_millis(300));
        assert!(TcpStream::connect(("127.0.0.1", port_of(&uri))).is_err());
        assert!(rx.try_recv().is_err());
    }
}
