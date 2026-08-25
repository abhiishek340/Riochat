//! The tab model: which tabs exist, which server each is bound to, which is
//! active, and what is persisted. Pure data; webview side effects live in
//! `shell.rs`.

use crate::origin::normalize_origin;
use crate::policy::Policy;
use crate::server::{Server, ServerMode};
use serde::{Deserialize, Serialize};
use std::path::Path;
use std::time::{Duration, Instant};

pub const WEBVIEW_PREFIX: &str = "tab-";
/// A tab hidden this long has its webview discarded.
pub const DISCARD_AFTER: Duration = Duration::from_secs(30 * 60);
/// How often the discard sweep runs.
pub const SWEEP_EVERY: Duration = Duration::from_secs(60);

#[derive(Debug, Clone)]
pub struct Tab {
    pub id: String,
    pub server: Option<Server>,
    /// Document title reported by the page (may be empty).
    pub title: String,
    pub hidden_since: Option<Instant>,
    /// Whether a webview currently exists for this tab.
    pub loaded: bool,
}

#[derive(Debug, Clone, Serialize, PartialEq, Eq)]
pub struct TabView {
    pub id: String,
    pub server: Option<Server>,
    pub title: String,
}

#[derive(Debug, Clone, Serialize, PartialEq, Eq)]
pub struct Snapshot {
    pub tabs: Vec<TabView>,
    pub active: Option<String>,
    pub platform: &'static str,
}

/// Result of closing a tab.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Closed {
    /// The server the closed tab was bound to.
    pub server: Option<Server>,
    /// The tab to activate afterwards when the active tab was closed.
    pub new_active: Option<String>,
    /// Id of a fresh empty tab created because the last tab was closed.
    pub replacement: Option<String>,
}

#[derive(Debug, Default)]
pub struct Model {
    tabs: Vec<Tab>,
    active: Option<String>,
    counter: u64,
}

pub fn platform_name() -> &'static str {
    if cfg!(target_os = "macos") {
        "macos"
    } else if cfg!(windows) {
        "windows"
    } else {
        "linux"
    }
}

impl Model {
    pub fn new() -> Self {
        Self::default()
    }

    /// Creates the tabs for `servers` (in order) or one empty tab when there
    /// are none, and activates `active` (clamped).
    pub fn from_servers(servers: &[Server], active: usize) -> Self {
        let mut m = Self::new();
        if servers.is_empty() {
            m.add(None);
        } else {
            for s in servers {
                m.add(Some(s.clone()));
            }
        }
        let idx = active.min(m.tabs.len() - 1);
        m.active = Some(m.tabs[idx].id.clone());
        m
    }

    pub fn tabs(&self) -> &[Tab] {
        &self.tabs
    }

    pub fn len(&self) -> usize {
        self.tabs.len()
    }

    pub fn is_empty(&self) -> bool {
        self.tabs.is_empty()
    }

    pub fn active(&self) -> Option<&str> {
        self.active.as_deref()
    }

    pub fn get(&self, id: &str) -> Option<&Tab> {
        self.tabs.iter().find(|t| t.id == id)
    }

    fn get_mut(&mut self, id: &str) -> Option<&mut Tab> {
        self.tabs.iter_mut().find(|t| t.id == id)
    }

    fn index_of(&self, id: &str) -> Result<usize, String> {
        self.tabs
            .iter()
            .position(|t| t.id == id)
            .ok_or_else(|| format!("unknown tab {id}"))
    }

    /// Appends a tab with a fresh label (`tab-1`, `tab-2`, ...; never reused).
    pub fn add(&mut self, server: Option<Server>) -> String {
        self.counter += 1;
        let id = format!("{WEBVIEW_PREFIX}{}", self.counter);
        self.tabs.push(Tab {
            id: id.clone(),
            server,
            title: String::new(),
            hidden_since: None,
            loaded: false,
        });
        id
    }

    pub fn find_by_server(&self, server: &Server) -> Option<&Tab> {
        self.tabs.iter().find(|t| t.server.as_ref() == Some(server))
    }

    pub fn uses_local(&self) -> bool {
        self.tabs
            .iter()
            .any(|t| t.server.as_ref().map(|s| s.mode == ServerMode::Local).unwrap_or(false))
    }

    /// Binds a tab to a server. A server is shown by at most one tab.
    pub fn bind(&mut self, id: &str, server: Server) -> Result<(), String> {
        let idx = self.index_of(id)?;
        match &self.tabs[idx].server {
            Some(existing) if *existing == server => return Ok(()),
            Some(_) => return Err("tab is already bound to a server".to_string()),
            None => {}
        }
        if self.tabs.iter().any(|t| t.server.as_ref() == Some(&server)) {
            return Err("that server is already open in another tab".to_string());
        }
        self.tabs[idx].server = Some(server);
        Ok(())
    }

    /// Detaches the tab's server and returns it.
    pub fn unbind(&mut self, id: &str) -> Result<Option<Server>, String> {
        let idx = self.index_of(id)?;
        self.tabs[idx].title.clear();
        Ok(self.tabs[idx].server.take())
    }

    /// Removes a tab. If it was active, its left neighbour (or the new first
    /// tab) becomes active. Closing the last tab leaves a fresh empty tab.
    pub fn close(&mut self, id: &str) -> Result<Closed, String> {
        let idx = self.index_of(id)?;
        let was_active = self.active.as_deref() == Some(id);
        let removed = self.tabs.remove(idx);
        let mut replacement = None;
        if self.tabs.is_empty() {
            replacement = Some(self.add(None));
        }
        let mut new_active = None;
        if was_active {
            let next = idx.saturating_sub(1).min(self.tabs.len() - 1);
            let next_id = self.tabs[next].id.clone();
            self.active = Some(next_id.clone());
            new_active = Some(next_id);
        }
        Ok(Closed {
            server: removed.server,
            new_active,
            replacement,
        })
    }

    /// Makes `id` active. Returns the previously active tab id when it changed.
    pub fn activate(&mut self, id: &str, now: Instant) -> Result<Option<String>, String> {
        self.index_of(id)?;
        let previous = self.active.replace(id.to_string());
        if let Some(prev) = previous.as_deref() {
            if prev != id {
                if let Some(t) = self.get_mut(prev) {
                    t.hidden_since = Some(now);
                }
            }
        }
        if let Some(t) = self.get_mut(id) {
            t.hidden_since = None;
        }
        Ok(previous.filter(|p| p != id))
    }

    /// Moves a tab to `index` (clamped to the end).
    pub fn move_tab(&mut self, id: &str, index: usize) -> Result<(), String> {
        let from = self.index_of(id)?;
        let tab = self.tabs.remove(from);
        let to = index.min(self.tabs.len());
        self.tabs.insert(to, tab);
        Ok(())
    }

    /// Records a page title. Returns whether anything changed.
    pub fn set_title(&mut self, id: &str, title: &str) -> bool {
        match self.get_mut(id) {
            Some(t) if t.title != title => {
                t.title = title.to_string();
                true
            }
            _ => false,
        }
    }

    pub fn set_loaded(&mut self, id: &str, loaded: bool, now: Instant) {
        let active = self.active.as_deref() == Some(id);
        if let Some(t) = self.get_mut(id) {
            t.loaded = loaded;
            if loaded && !active && t.hidden_since.is_none() {
                t.hidden_since = Some(now);
            }
        }
    }

    /// Tabs whose webview should be discarded: loaded, not active, hidden for
    /// at least `ttl`.
    pub fn discard_candidates(&self, now: Instant, ttl: Duration) -> Vec<String> {
        self.tabs
            .iter()
            .filter(|t| {
                t.loaded
                    && self.active.as_deref() != Some(t.id.as_str())
                    && t.hidden_since.map(|h| now.duration_since(h) >= ttl).unwrap_or(false)
            })
            .map(|t| t.id.clone())
            .collect()
    }

    pub fn snapshot(&self) -> Snapshot {
        Snapshot {
            tabs: self
                .tabs
                .iter()
                .map(|t| TabView {
                    id: t.id.clone(),
                    server: t.server.clone(),
                    title: t.title.clone(),
                })
                .collect(),
            active: self.active.clone(),
            platform: platform_name(),
        }
    }

    /// Bound tabs in order plus the active position among them (0 when the
    /// active tab is unbound).
    pub fn persisted(&self) -> Persisted {
        let mut servers = Vec::new();
        let mut active = 0usize;
        for t in &self.tabs {
            if let Some(s) = &t.server {
                if self.active.as_deref() == Some(t.id.as_str()) {
                    active = servers.len();
                }
                servers.push(s.clone());
            }
        }
        Persisted { servers, active }
    }
}
