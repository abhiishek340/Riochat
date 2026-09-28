//! Download bookkeeping. The platform saves downloads to the user's Downloads
//! folder; the shell only vets the proposed destination and reports the end.

use serde::Serialize;
use std::collections::HashMap;
use std::path::Path;
use std::sync::Mutex;

/// Payload of the `downloads:finished` event.
#[derive(Debug, Clone, Serialize, PartialEq, Eq)]
#[serde(rename_all = "camelCase")]
pub struct Finished {
    pub file_name: String,
    pub success: bool,
}

/// A destination is acceptable when it is absolute and its folder exists (the
/// OS reports no folder when there is no Downloads directory).
pub fn destination_ok(path: &Path) -> bool {
    path.is_absolute() && path.parent().map(|p| p.is_dir()).unwrap_or(false)
}

/// The final component of a path, or an empty string.
pub fn file_name_of(path: &Path) -> String {
    path.file_name()
        .map(|n| n.to_string_lossy().into_owned())
        .unwrap_or_default()
}

/// Remembers the name proposed at request time, per download URL, for
/// platforms that report no path when the download finishes (macOS).
#[derive(Default)]
pub struct Pending {
    names: Mutex<HashMap<String, String>>,
}

impl Pending {
    pub fn note(&self, url: &str, file_name: String) {
        if let Ok(mut m) = self.names.lock() {
            m.insert(url.to_string(), file_name);
        }
    }

    pub fn take(&self, url: &str) -> Option<String> {
        self.names.lock().ok().and_then(|mut m| m.remove(url))
    }

    /// Builds the finish payload: the saved name when the OS reports a path
    /// (it includes any de-duplication suffix), else the name noted earlier.
    pub fn finish(&self, url: &str, saved: Option<&Path>, success: bool) -> Finished {
        let noted = self.take(url);
        let file_name = match saved.map(file_name_of) {
            Some(n) if !n.is_empty() => n,
            _ => noted.unwrap_or_default(),
        };
        Finished { file_name, success }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::path::PathBuf;

    #[test]
    fn destination_rules() {
        let dir = tempfile::tempdir().unwrap();
        assert!(destination_ok(&dir.path().join("a.txt")));
        assert!(!destination_ok(Path::new("relative/a.txt")));
        assert!(!destination_ok(&dir.path().join("missing").join("a.txt")));
        assert!(!destination_ok(Path::new("")));
    }

    #[test]
    fn names() {
        assert_eq!(file_name_of(Path::new("/x/y/report (1).pdf")), "report (1).pdf");
        assert_eq!(file_name_of(Path::new("/")), "");
        assert_eq!(file_name_of(Path::new("")), "");
    }

    #[test]
    fn finish_prefers_saved_name_and_falls_back_to_noted() {
        let p = Pending::default();
        p.note("https://a/b", "b.zip".into());
        let f = p.finish("https://a/b", Some(&PathBuf::from("/dl/b (1).zip")), true);
        assert_eq!(
            f,
            Finished {
                file_name: "b (1).zip".into(),
                success: true
            }
        );
        // macOS: no path on finish
        p.note("https://a/c", "c.zip".into());
        let f = p.finish("https://a/c", None, false);
        assert_eq!(
            f,
            Finished {
                file_name: "c.zip".into(),
                success: false
            }
        );
        // a path without a file name yields an empty name
        let f = p.finish("https://a/d", Some(Path::new("/")), true);
        assert_eq!(f.file_name, "");
        let v = serde_json::to_value(&f).unwrap();
        assert_eq!(v["fileName"], "");
        assert_eq!(v["success"], true);
    }
}
