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
