//! The bundled Riochat server ("local mode"), run on the loopback interface.
//!
//! The shell launches `riochat-server --local --data-dir <dir>`; the server
//! prints one JSON line (`type: ready`) carrying its origin and a one-time
//! sign-in grant. At most one process runs at a time.

use crate::error::ShellError;
use serde::Deserialize;
use std::fmt;
use std::path::PathBuf;
use std::sync::{Arc, Mutex};
use std::time::Duration;
use tauri::async_runtime::Receiver;
use tauri::AppHandle;
use tauri_plugin_shell::process::{CommandChild, CommandEvent};
use tauri_plugin_shell::ShellExt;

pub const PROGRAM: &str = "riochat-server";
pub const READY_TIMEOUT: Duration = Duration::from_secs(30);

/// The handshake line the server prints once it is listening.
#[derive(Clone, Deserialize)]
pub struct Ready {
    #[serde(rename = "type")]
    pub kind: String,
    #[serde(default)]
    pub version: String,
    pub origin: String,
    #[serde(default)]
    pub grant: String,
    #[serde(default)]
    pub pid: u64,
}

// The grant is a credential; never print it.
impl fmt::Debug for Ready {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("Ready")
            .field("version", &self.version)
            .field("origin", &self.origin)
            .field("grant", &"<redacted>")
            .field("pid", &self.pid)
            .finish()
    }
}

/// Parses one stdout line. Lines that are not JSON or not `ready` are ignored.
pub fn parse_ready_line(line: &str) -> Option<Ready> {
    let ready: Ready = serde_json::from_str(line.trim()).ok()?;
    (ready.kind == "ready" && !ready.origin.is_empty()).then_some(ready)
}

/// Waits for the handshake. Stderr lines are forwarded to the log. Failure
/// cases: a launch error, termination before ready, output closing before
/// ready, and the time limit.
pub async fn wait_for_ready(rx: &mut Receiver<CommandEvent>, limit: Duration) -> Result<Ready, String> {
    let wait = async {
        loop {
            match rx.recv().await {
                Some(CommandEvent::Stdout(bytes)) => {
                    if let Some(ready) = parse_ready_line(&String::from_utf8_lossy(&bytes)) {
                        return Ok(ready);
                    }
                }
                Some(CommandEvent::Stderr(bytes)) => forward_stderr(&bytes),
                Some(CommandEvent::Error(e)) => return Err(format!("sidecar failed to run: {e}")),
                Some(CommandEvent::Terminated(p)) => {
                    let code = p.code.map(|c| c.to_string()).unwrap_or_else(|| "unknown".to_string());
                    return Err(format!("sidecar exited before ready (code {code})"));
                }
                Some(_) => {}
                None => return Err("sidecar output closed before ready".to_string()),
            }
        }
    };
    match tokio::time::timeout(limit, wait).await {
        Ok(result) => result,
        Err(_) => Err("sidecar did not become ready in time".to_string()),
    }
}

fn forward_stderr(bytes: &[u8]) {
    let text = String::from_utf8_lossy(bytes);
    let line = text.trim_end();
    if !line.is_empty() {
        log::info!("[sidecar] {line}");
    }
}

/// How to launch the server.
#[derive(Debug, Clone)]
pub struct Launch {
    pub data_dir: PathBuf,
    pub env: Vec<(String, String)>,
}

struct Info {
    origin: String,
    pid: u32,
    grant: Option<String>,
}

/// Manages the (at most one) running server.
#[derive(Default)]
pub struct Sidecar {
    /// Serializes starts and grant requests.
    gate: tokio::sync::Mutex<()>,
    info: Mutex<Option<Info>>,
    child: Mutex<Option<CommandChild>>,
}

impl Sidecar {
    pub fn new() -> Arc<Self> {
        Arc::new(Self::default())
    }

    pub fn is_running(&self) -> bool {
        self.info.lock().map(|i| i.is_some()).unwrap_or(false)
    }

    /// Returns the origin of the running server, starting one if needed.
    pub async fn ensure_running(self: &Arc<Self>, app: &AppHandle, launch: &Launch) -> Result<String, ShellError> {
        let _gate = self.gate.lock().await;
        if let Some(origin) = self.current_origin() {
            return Ok(origin);
        }
        self.start_locked(app, launch).await
    }

    /// Takes the single-use sign-in grant, restarting the server when the grant
    /// was already consumed or no server runs. Returns `(origin, grant)`.
    pub async fn take_grant(
        self: &Arc<Self>,
        app: &AppHandle,
        launch: &Launch,
    ) -> Result<(String, String), ShellError> {
        let _gate = self.gate.lock().await;
        if let Some(found) = self.take_current_grant() {
            return Ok(found);
        }
        // consumed or absent: a fresh process gives a fresh grant
        self.kill_now();
        self.start_locked(app, launch).await?;
        self.take_current_grant()
            .ok_or_else(|| ShellError::sidecar("the local server did not provide a sign-in grant"))
    }

    fn current_origin(&self) -> Option<String> {
        self.info.lock().ok()?.as_ref().map(|i| i.origin.clone())
    }

    fn take_current_grant(&self) -> Option<(String, String)> {
        let mut guard = self.info.lock().ok()?;
        let info = guard.as_mut()?;
        let grant = info.grant.take()?;
        Some((info.origin.clone(), grant))
    }

    async fn start_locked(self: &Arc<Self>, app: &AppHandle, launch: &Launch) -> Result<String, ShellError> {
        std::fs::create_dir_all(&launch.data_dir)
            .map_err(|e| ShellError::sidecar(format!("cannot create the local data folder: {e}")))?;
        let data_dir = launch.data_dir.to_string_lossy().into_owned();
        let mut command = app
            .shell()
            .sidecar(PROGRAM)
            .map_err(|e| ShellError::sidecar(format!("cannot find the local server: {e}")))?
            .args(["--local", "--data-dir", data_dir.as_str()]);
        for (k, v) in &launch.env {
            command = command.env(k, v);
        }
        let (mut rx, child) = command
            .spawn()
            .map_err(|e| ShellError::sidecar(format!("cannot start the local server: {e}")))?;
        let pid = child.pid();
        let ready = match wait_for_ready(&mut rx, READY_TIMEOUT).await {
            Ok(r) => r,
            Err(message) => {
                let _ = child.kill();
                return Err(ShellError::sidecar(message));
            }
        };
        if let Ok(mut slot) = self.child.lock() {
            *slot = Some(child);
        }
        if let Ok(mut slot) = self.info.lock() {
            *slot = Some(Info {
                origin: ready.origin.clone(),
                pid,
                grant: Some(ready.grant.clone()).filter(|g| !g.is_empty()),
            });
        }
        // keep draining output so the pipe never fills, and notice an exit
        let me = Arc::clone(self);
        tauri::async_runtime::spawn(async move {
            while let Some(event) = rx.recv().await {
                match event {
                    CommandEvent::Stderr(b) => forward_stderr(&b),
                    CommandEvent::Error(e) => log::warn!("[sidecar] {e}"),
                    CommandEvent::Terminated(_) => break,
                    _ => {}
                }
            }
            me.forget(pid);
        });
        Ok(ready.origin)
    }

    /// Forgets the server when it exited on its own (matched by process id).
    fn forget(&self, pid: u32) {
        if let Ok(mut info) = self.info.lock() {
            if info.as_ref().map(|i| i.pid) == Some(pid) {
                *info = None;
                if let Ok(mut child) = self.child.lock() {
                    *child = None;
                }
                log::info!("[sidecar] process {pid} exited");
            }
        }
    }

    /// Synchronous best-effort kill that never waits on the start lock. Used
    /// for restarts and at application exit.
    pub fn kill_now(&self) {
        if let Ok(mut info) = self.info.lock() {
            *info = None;
        }
        if let Ok(mut child) = self.child.lock() {
            if let Some(c) = child.take() {
                let _ = c.kill();
            }
        }
    }

    pub fn stop(&self) {
        self.kill_now();
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use tauri::async_runtime::{block_on, channel};
    use tauri_plugin_shell::process::TerminatedPayload;

    const READY: &str =
        r#"{"type":"ready","version":"0.4.5","origin":"http://127.0.0.1:54321","grant":"g-secret","pid":99}"#;

    #[test]
    fn parses_only_ready_lines() {
        let r = parse_ready_line(READY).unwrap();
        assert_eq!(r.origin, "http://127.0.0.1:54321");
        assert_eq!(r.grant, "g-secret");
        assert_eq!(r.pid, 99);
        assert!(parse_ready_line("starting up...").is_none());
        assert!(parse_ready_line(r#"{"type":"log","msg":"x"}"#).is_none());
        assert!(parse_ready_line(r#"{"type":"ready","origin":""}"#).is_none());
        assert!(parse_ready_line("  ").is_none());
        assert!(!format!("{r:?}").contains("g-secret"));
    }

    fn feed(events: Vec<CommandEvent>, keep_open: bool) -> Receiver<CommandEvent> {
        let (tx, rx) = channel(16);
        block_on(async {
            for e in events {
                tx.send(e).await.unwrap();
            }
        });
        if keep_open {
            std::mem::forget(tx);
        }
        rx
    }

    fn out(s: &str) -> CommandEvent {
        CommandEvent::Stdout(s.as_bytes().to_vec())
    }

    #[test]
    fn ready_after_noise() {
        let mut rx = feed(
            vec![
                out("not json"),
                out(r#"{"type":"log"}"#),
                CommandEvent::Stderr(b"warn: something".to_vec()),
                out(READY),
            ],
            true,
        );
        let r = block_on(wait_for_ready(&mut rx, Duration::from_secs(2))).unwrap();
        assert_eq!(r.origin, "http://127.0.0.1:54321");
    }

    #[test]
    fn failure_cases() {
        let mut rx = feed(vec![CommandEvent::Error("spawn failed".into())], true);
        assert!(block_on(wait_for_ready(&mut rx, Duration::from_secs(2)))
            .unwrap_err()
            .contains("spawn failed"));

        let mut rx = feed(
            vec![CommandEvent::Terminated(TerminatedPayload {
                code: Some(3),
                signal: None,
            })],
            true,
        );
        assert_eq!(
            block_on(wait_for_ready(&mut rx, Duration::from_secs(2))).unwrap_err(),
            "sidecar exited before ready (code 3)"
        );

        let mut rx = feed(vec![], false);
        assert_eq!(
            block_on(wait_for_ready(&mut rx, Duration::from_secs(2))).unwrap_err(),
            "sidecar output closed before ready"
        );

        let mut rx = feed(vec![out("nothing useful")], true);
        assert_eq!(
            block_on(wait_for_ready(&mut rx, Duration::from_millis(100))).unwrap_err(),
            "sidecar did not become ready in time"
        );
    }

    #[test]
    fn grant_is_single_use_in_memory() {
        let s = Sidecar::default();
        *s.info.lock().unwrap() = Some(Info {
            origin: "http://127.0.0.1:1".into(),
            pid: 7,
            grant: Some("g".into()),
        });
        assert!(s.is_running());
        assert_eq!(
            s.take_current_grant(),
            Some(("http://127.0.0.1:1".to_string(), "g".to_string()))
        );
        assert_eq!(s.take_current_grant(), None);
        assert_eq!(s.current_origin().as_deref(), Some("http://127.0.0.1:1"));
        // a different pid exiting does not clear it
        s.forget(8);
        assert!(s.is_running());
        s.forget(7);
        assert!(!s.is_running());
    }
}
