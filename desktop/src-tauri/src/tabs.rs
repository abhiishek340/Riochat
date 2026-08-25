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

/// Content of `tabs.json`.
#[derive(Debug, Clone, Default, Serialize, Deserialize, PartialEq, Eq)]
pub struct Persisted {
    pub servers: Vec<Server>,
    #[serde(default)]
    pub active: usize,
}

impl Persisted {
    /// Missing file: empty. Corrupt file: empty, with a log line.
    pub fn load(path: &Path) -> Persisted {
        match std::fs::read(path) {
            Ok(bytes) => serde_json::from_slice(&bytes).unwrap_or_else(|e| {
                log::warn!("tabs.json is unreadable ({e}); starting with no saved tabs");
                Persisted::default()
            }),
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => Persisted::default(),
            Err(e) => {
                log::warn!("cannot read tabs.json ({e}); starting with no saved tabs");
                Persisted::default()
            }
        }
    }

    pub fn save(&self, path: &Path) -> std::io::Result<()> {
        let bytes = serde_json::to_vec(self).map_err(std::io::Error::other)?;
        crate::fsutil::atomic_write(path, &bytes)
    }
}

/// The outcome of sanitizing saved tabs.
#[derive(Debug, PartialEq, Eq)]
pub struct Restored {
    pub servers: Vec<Server>,
    pub active: usize,
    /// Entries dropped because current policy forbids them; their stored
    /// credentials must be deleted.
    pub forbidden: Vec<Server>,
}

/// Drops remote entries whose origin is not already normalized, duplicates,
/// and entries policy forbids; keeps the active tab pointing at the same entry
/// where possible.
pub fn sanitize(saved: &Persisted, policy: &Policy) -> Restored {
    let mut servers: Vec<Server> = Vec::new();
    let mut forbidden: Vec<Server> = Vec::new();
    let mut active_server: Option<Server> = None;
    for (i, s) in saved.servers.iter().enumerate() {
        let valid = match s.mode {
            ServerMode::Local => true,
            ServerMode::Remote => normalize_origin(&s.origin).as_deref() == Some(s.origin.as_str()),
        };
        // normalize the local entry's origin so duplicates compare equal
        let s = if s.mode == ServerMode::Local {
            Server::local()
        } else {
            s.clone()
        };
        if !valid || servers.contains(&s) {
            continue;
        }
        if policy.check_server(&s).is_err() {
            if !forbidden.contains(&s) {
                forbidden.push(s);
            }
            continue;
        }
        if i == saved.active {
            active_server = Some(s.clone());
        }
        servers.push(s);
    }
    let active = active_server
        .and_then(|a| servers.iter().position(|s| *s == a))
        .unwrap_or_else(|| saved.active.min(servers.len().saturating_sub(1)));
    Restored {
        servers,
        active,
        forbidden,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn remote(n: &str) -> Server {
        Server::remote(format!("https://{n}.example"))
    }

    /// [local (bound), empty]
    fn two() -> Model {
        let mut m = Model::from_servers(&[Server::local()], 0);
        m.add(None);
        m
    }

    #[test]
    fn binding_rules() {
        let mut m = two();
        let (a, b) = (m.tabs()[0].id.clone(), m.tabs()[1].id.clone());
        assert_eq!(
            m.bind(&b, Server::local()).unwrap_err(),
            "that server is already open in another tab"
        );
        m.bind(&b, remote("x")).unwrap();
        m.bind(&b, remote("x")).unwrap(); // no-op
        assert_eq!(m.bind(&b, remote("y")).unwrap_err(), "tab is already bound to a server");
        assert_eq!(m.bind("tab-99", remote("z")).unwrap_err(), "unknown tab tab-99");
        assert_eq!(m.bind(&a, remote("x")).unwrap_err(), "tab is already bound to a server");
    }

    #[test]
    fn labels_are_never_reused() {
        let mut m = Model::from_servers(&[], 0);
        let first = m.tabs()[0].id.clone();
        assert_eq!(first, "tab-1");
        let second = m.add(None);
        m.activate(&second, Instant::now()).unwrap();
        m.close(&first).unwrap();
        assert_eq!(m.add(None), "tab-3");
    }

    #[test]
    fn closing_picks_the_left_neighbour() {
        let mut m = Model::from_servers(&[remote("a"), remote("b"), remote("c")], 1);
        let ids: Vec<String> = m.tabs().iter().map(|t| t.id.clone()).collect();
        // active middle tab -> left neighbour
        let c = m.close(&ids[1]).unwrap();
        assert_eq!(c.new_active.as_deref(), Some(ids[0].as_str()));
        assert_eq!(m.active(), Some(ids[0].as_str()));
        // active first tab -> the new first
        let c = m.close(&ids[0]).unwrap();
        assert_eq!(c.new_active.as_deref(), Some(ids[2].as_str()));
        // closing a non-active tab keeps the active one
        let mut m = Model::from_servers(&[remote("a"), remote("b")], 0);
        let ids: Vec<String> = m.tabs().iter().map(|t| t.id.clone()).collect();
        let c = m.close(&ids[1]).unwrap();
        assert_eq!(c.new_active, None);
        assert_eq!(m.active(), Some(ids[0].as_str()));
        assert_eq!(c.server, Some(remote("b")));
    }

    #[test]
    fn closing_the_last_tab_leaves_a_fresh_one() {
        let mut m = Model::from_servers(&[remote("a")], 0);
        let id = m.tabs()[0].id.clone();
        let c = m.close(&id).unwrap();
        assert_eq!(m.len(), 1);
        assert!(m.tabs()[0].server.is_none());
        assert_eq!(c.replacement.as_deref(), Some(m.tabs()[0].id.as_str()));
        assert_eq!(m.active(), c.new_active.as_deref());
        assert_ne!(m.tabs()[0].id, id);
        assert_eq!(m.close("nope").unwrap_err(), "unknown tab nope");
    }

    #[test]
    fn moving() {
        let mut m = Model::from_servers(&[remote("a"), remote("b"), remote("c")], 0);
        let ids: Vec<String> = m.tabs().iter().map(|t| t.id.clone()).collect();
        m.move_tab(&ids[0], 1).unwrap();
        let order: Vec<_> = m.tabs().iter().map(|t| t.id.clone()).collect();
        assert_eq!(order, vec![ids[1].clone(), ids[0].clone(), ids[2].clone()]);
        m.move_tab(&ids[1], 99).unwrap();
        assert_eq!(m.tabs().last().unwrap().id, ids[1]);
        assert_eq!(m.move_tab("zzz", 0).unwrap_err(), "unknown tab zzz");
    }

    #[test]
    fn persistence_skips_unbound_and_tracks_active() {
        let mut m = Model::from_servers(&[remote("a"), remote("b")], 1);
        m.add(None);
        assert_eq!(
            m.persisted(),
            Persisted {
                servers: vec![remote("a"), remote("b")],
                active: 1
            }
        );
        let unbound = m.tabs()[2].id.clone();
        m.activate(&unbound, Instant::now()).unwrap();
        assert_eq!(m.persisted().active, 0);
    }

    #[test]
    fn unbind_returns_server_and_clears_title() {
        let mut m = Model::from_servers(&[remote("a")], 0);
        let id = m.tabs()[0].id.clone();
        m.set_title(&id, "Chat");
        assert_eq!(m.unbind(&id).unwrap(), Some(remote("a")));
        assert!(m.get(&id).unwrap().server.is_none());
        assert_eq!(m.get(&id).unwrap().title, "");
        assert_eq!(m.unbind("x").unwrap_err(), "unknown tab x");
    }

    #[test]
    fn uses_local_and_lookup() {
        let m = two();
        assert!(m.uses_local());
        assert!(m.find_by_server(&Server::local()).is_some());
        assert!(m.find_by_server(&remote("q")).is_none());
    }

    #[test]
    fn discard_sweep() {
        let t0 = Instant::now();
        let mut m = Model::from_servers(&[remote("a"), remote("b")], 0);
        let ids: Vec<String> = m.tabs().iter().map(|t| t.id.clone()).collect();
        m.set_loaded(&ids[0], true, t0);
        m.set_loaded(&ids[1], true, t0);
        let later = t0 + DISCARD_AFTER + Duration::from_secs(1);
        // the active tab is never discarded; the other has been hidden since load
        assert_eq!(m.discard_candidates(later, DISCARD_AFTER), vec![ids[1].clone()]);
        assert!(m
            .discard_candidates(t0 + Duration::from_secs(5), DISCARD_AFTER)
            .is_empty());
        // activating it resets the clock, hiding the first starts one
        m.activate(&ids[1], later).unwrap();
        assert!(m.discard_candidates(later, DISCARD_AFTER).is_empty());
        m.set_loaded(&ids[1], false, later);
        assert!(m.discard_candidates(later + DISCARD_AFTER * 2, DISCARD_AFTER) == vec![ids[0].clone()]);
    }

    #[test]
    fn snapshot_shape() {
        let mut m = Model::from_servers(&[remote("a")], 0);
        let id = m.tabs()[0].id.clone();
        m.set_title(&id, "Hello");
        let v = serde_json::to_value(m.snapshot()).unwrap();
        assert_eq!(v["active"], id);
        assert_eq!(v["tabs"][0]["title"], "Hello");
        assert_eq!(v["tabs"][0]["server"]["mode"], "remote");
        assert!(["macos", "windows", "linux"].contains(&v["platform"].as_str().unwrap()));
        let empty = Model::from_servers(&[], 0);
        assert!(serde_json::to_value(empty.snapshot()).unwrap()["tabs"][0]["server"].is_null());
    }

    #[test]
    fn sanitize_drops_bad_dupes_and_forbidden() {
        let saved = Persisted {
            servers: vec![
                Server::local(),
                remote("a"),
                Server::remote("HTTPS://UPPER.example"),
                remote("a"),
                Server::remote("https://x/path"),
                remote("b"),
            ],
            active: 5,
        };
        let r = sanitize(&saved, &Policy::default());
        assert_eq!(r.servers, vec![Server::local(), remote("a"), remote("b")]);
        assert_eq!(r.active, 2);
        assert!(r.forbidden.is_empty());

        let locked = Policy {
            default_server_url: Some("https://a.example".into()),
            server_url_locked: true,
            ..Policy::default()
        };
        let r = sanitize(&saved, &locked);
        assert_eq!(r.servers, vec![remote("a")]);
        assert_eq!(r.forbidden, vec![Server::local(), remote("b")]);
        assert_eq!(r.active, 0);

        let r = sanitize(&Persisted::default(), &Policy::default());
        assert!(r.servers.is_empty());
        assert_eq!(r.active, 0);
    }

    #[test]
    fn file_round_trip_corrupt_and_missing() {
        let dir = tempfile::tempdir().unwrap();
        let p = dir.path().join("tabs.json");
        assert_eq!(Persisted::load(&p), Persisted::default());
        let saved = Persisted {
            servers: vec![Server::local(), remote("a")],
            active: 1,
        };
        saved.save(&p).unwrap();
        assert_eq!(Persisted::load(&p), saved);
        let text = std::fs::read_to_string(&p).unwrap();
        assert!(text.contains(r#""servers":[{"mode":"local","origin":""}"#), "{text}");
        std::fs::write(&p, b"garbage").unwrap();
        assert_eq!(Persisted::load(&p), Persisted::default());
    }

    #[test]
    fn from_servers_clamps_active() {
        let m = Model::from_servers(&[remote("a"), remote("b")], 99);
        assert_eq!(m.active(), Some(m.tabs()[1].id.as_str()));
    }
}
