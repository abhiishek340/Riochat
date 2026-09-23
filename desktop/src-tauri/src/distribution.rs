//! Distribution kind, update mode and the download page URL.

use crate::policy::{Policy, PolicyInfo};
use serde::Serialize;

pub const DEFAULT_REPOSITORY: &str = "https://github.com/riochat/riochat";

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "lowercase")]
pub enum Kind {
    Installer,
    Msi,
    Portable,
    Dev,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "lowercase")]
pub enum UpdateMode {
    Auto,
    Notify,
    Disabled,
}

/// What the bundler embedded in the executable.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Bundle {
    /// No bundle type was embedded.
    Unknown,
    Msi,
    /// NSIS, app, dmg, AppImage, deb or rpm.
    Other,
}

/// Portable marker first, then debug build, then the embedded bundle type.
pub fn classify(portable_marker: bool, debug_build: bool, bundle: Bundle) -> Kind {
    if portable_marker {
        Kind::Portable
    } else if debug_build {
        Kind::Dev
    } else {
        match bundle {
            Bundle::Unknown => Kind::Dev,
            Bundle::Msi => Kind::Msi,
            Bundle::Other => Kind::Installer,
        }
    }
}

pub fn update_mode(kind: Kind, disabled_by_policy: bool) -> UpdateMode {
    if disabled_by_policy {
        return UpdateMode::Disabled;
    }
    match kind {
        Kind::Installer => UpdateMode::Auto,
        Kind::Portable => UpdateMode::Notify,
        Kind::Msi | Kind::Dev => UpdateMode::Disabled,
    }
}

/// Derives the release page from the first updater endpoint. A GitHub release
/// endpoint gives that repository; anything else the default repository.
/// Stable versions point at `releases/latest`, prereleases at `releases`.
pub fn releases_url(first_endpoint: Option<&str>, version: &str) -> String {
    let base = first_endpoint
        .and_then(github_base)
        .unwrap_or_else(|| DEFAULT_REPOSITORY.to_string());
    let prerelease = semver::Version::parse(version)
        .map(|v| !v.pre.is_empty())
        .unwrap_or(false);
    if prerelease {
        format!("{base}/releases")
    } else {
        format!("{base}/releases/latest")
    }
}

fn github_base(endpoint: &str) -> Option<String> {
    let url = url::Url::parse(endpoint).ok()?;
    if url.scheme() != "https" || url.host_str()? != "github.com" {
        return None;
    }
    let segs: Vec<&str> = url.path_segments()?.collect();
    if segs.len() >= 3 && !segs[0].is_empty() && !segs[1].is_empty() && segs[2] == "releases" {
        Some(format!("https://github.com/{}/{}", segs[0], segs[1]))
    } else {
        None
    }
}

/// The `get_distribution` payload.
#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Distribution {
    pub kind: Kind,
    pub update_mode: UpdateMode,
    pub releases_url: String,
    pub policy: PolicyInfo,
}

impl Distribution {
    pub fn new(kind: Kind, mode: UpdateMode, releases_url: String, policy: &Policy) -> Self {
        Self {
            kind,
            update_mode: mode,
            releases_url,
            policy: policy.info(),
        }
    }
}

/// The error returned by update commands when checks are not allowed.
pub fn disabled_message(policy: &Policy) -> crate::error::ShellError {
    match policy.check_update() {
        Err(e) => e,
        Ok(()) => crate::error::ShellError::new(
            crate::error::ErrorKind::Policy,
            "update checks are disabled for this distribution",
        ),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn kind_mapping() {
        assert_eq!(classify(true, true, Bundle::Msi), Kind::Portable);
        assert_eq!(classify(false, true, Bundle::Other), Kind::Dev);
        assert_eq!(classify(false, false, Bundle::Unknown), Kind::Dev);
        assert_eq!(classify(false, false, Bundle::Msi), Kind::Msi);
        assert_eq!(classify(false, false, Bundle::Other), Kind::Installer);
    }

    #[test]
    fn mode_mapping_and_policy_override() {
        assert_eq!(update_mode(Kind::Installer, false), UpdateMode::Auto);
        assert_eq!(update_mode(Kind::Portable, false), UpdateMode::Notify);
        assert_eq!(update_mode(Kind::Msi, false), UpdateMode::Disabled);
        assert_eq!(update_mode(Kind::Dev, false), UpdateMode::Disabled);
        for k in [Kind::Installer, Kind::Portable, Kind::Msi, Kind::Dev] {
            assert_eq!(update_mode(k, true), UpdateMode::Disabled);
        }
    }

    #[test]
    fn release_urls() {
        let ep = "https://github.com/acme/chat/releases/latest/download/latest.json";
        assert_eq!(
            releases_url(Some(ep), "1.2.3"),
            "https://github.com/acme/chat/releases/latest"
        );
        assert_eq!(
            releases_url(Some(ep), "1.2.3-beta.1"),
            "https://github.com/acme/chat/releases"
        );
        assert_eq!(
            releases_url(None, "1.0.0"),
            "https://github.com/riochat/riochat/releases/latest"
        );
        for other in [
            "https://example.com/acme/chat/releases/x",
            "http://github.com/acme/chat/releases/x",
            "https://github.com/acme/chat/raw/main/x",
            "not a url",
        ] {
            assert_eq!(
                releases_url(Some(other), "1.0.0-rc.1"),
                "https://github.com/riochat/riochat/releases",
                "{other}"
            );
        }
    }

    #[test]
    fn disabled_messages() {
        let mut p = Policy::default();
        assert_eq!(
            disabled_message(&p).message,
            "update checks are disabled for this distribution"
        );
        p.auto_update_disabled = true;
        assert_eq!(
            disabled_message(&p).message,
            "policy: update checks are disabled by your administrator"
        );
    }

    #[test]
    fn payload_shape() {
        let d = Distribution::new(Kind::Portable, UpdateMode::Notify, "u".into(), &Policy::default());
        let v = serde_json::to_value(d).unwrap();
        assert_eq!(v["kind"], "portable");
        assert_eq!(v["updateMode"], "notify");
        assert_eq!(v["releasesUrl"], "u");
        assert!(v["policy"].is_object());
    }
}
