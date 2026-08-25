//! The native window: geometry, creation and navigation containment.

use tauri::webview::NewWindowResponse;
use tauri::{AppHandle, LogicalPosition, LogicalSize, Manager, Position, Rect, Size, Window, Wry};
use url::Url;

pub const MAIN_LABEL: &str = "main";
pub const STRIP_LABEL: &str = "chrome";
pub const STRIP_HEIGHT: f64 = 38.0;
pub const STRIP_ROUTE: &str = "desktop/tabs";
pub const TAB_ROUTE: &str = "chat";

/// `(x, y, width, height)` in logical pixels.
pub type Bounds = (f64, f64, f64, f64);

/// The strip spans the full width at the top; the content fills the rest.
pub fn compute_layout(width: f64, height: f64) -> (Bounds, Bounds) {
    let w = width.max(0.0);
    let strip_h = STRIP_HEIGHT.min(height.max(0.0));
    let strip = (0.0, 0.0, w, strip_h);
    let content = (0.0, strip_h, w, (height - strip_h).max(0.0));
    (strip, content)
}

pub fn to_rect(b: Bounds) -> Rect {
    Rect {
        position: Position::Logical(LogicalPosition::new(b.0, b.1)),
        size: Size::Logical(LogicalSize::new(b.2, b.3)),
    }
}

/// Current strip and content rectangles for the window.
pub fn layout_for(window: &Window) -> tauri::Result<(Bounds, Bounds)> {
    let scale = window.scale_factor()?;
    let size = window.inner_size()?.to_logical::<f64>(scale);
    Ok(compute_layout(size.width, size.height))
}

/// Navigation is limited to the app itself: the `tauri:` scheme, `about:` and
/// `blob:` URLs, and http(s) pages on `tauri.localhost` (the Windows app
/// origin). Debug builds also allow the dev server on localhost.
pub fn navigation_allowed(url: &Url, debug_build: bool) -> bool {
    match url.scheme() {
        "tauri" | "about" | "blob" => true,
        "http" | "https" => match url.host_str() {
            Some("tauri.localhost") => true,
            Some("localhost") | Some("127.0.0.1") => debug_build,
            _ => false,
        },
        _ => false,
    }
}

pub fn is_external_web_url(url: &Url) -> bool {
    matches!(url.scheme(), "http" | "https")
}

/// Handler for requests to open a new window: web links go to the system
/// browser, nothing ever opens a new webview window.
pub fn deny_new_window(app: &AppHandle, url: Url) -> NewWindowResponse<Wry> {
    if is_external_web_url(&url) {
        use tauri_plugin_opener::OpenerExt;
        if let Err(e) = app.opener().open_url(url.as_str(), None::<&str>) {
            log::warn!("cannot open a link in the system browser: {e}");
        }
    }
    NewWindowResponse::Deny
}

/// Brings the main window forward: show, unminimize if needed, then focus.
pub fn show_main(app: &AppHandle) {
    if let Some(w) = app.get_window(MAIN_LABEL) {
        let _ = w.show();
        if w.is_minimized().unwrap_or(false) {
            let _ = w.unminimize();
        }
        let _ = w.set_focus();
    }
}

/// Creates the main window (no webviews yet).
pub fn create_main(app: &AppHandle) -> tauri::Result<Window> {
    use tauri::window::WindowBuilder;
    let builder = WindowBuilder::new(app, MAIN_LABEL)
        .title("Riochat")
        .inner_size(1280.0, 840.0)
        .min_inner_size(960.0, 640.0)
        .center();
    #[cfg(target_os = "macos")]
    let builder = builder
        .title_bar_style(tauri::TitleBarStyle::Overlay)
        .hidden_title(true);
    #[cfg(windows)]
    let builder = builder.decorations(false).shadow(true);
    let window = builder.build()?;
    #[cfg(target_os = "macos")]
    mac::compact_toolbar(&window);
    Ok(window)
}

#[cfg(target_os = "macos")]
pub mod mac {
    use objc2::MainThreadMarker;
    use objc2_app_kit::{NSToolbar, NSWindow, NSWindowToolbarStyle};
    use objc2_foundation::NSString;
    use tauri::Window;

    /// An empty compact toolbar makes the traffic lights sit vertically
    /// centered on the 38 px strip. The title band itself receives no pointer
    /// events because the strip webview covers it.
    pub fn compact_toolbar(window: &Window) {
        let Ok(ptr) = window.ns_window() else { return };
        let Some(mtm) = MainThreadMarker::new() else {
            log::warn!("toolbar setup must run on the main thread");
            return;
        };
        // SAFETY: tao hands out a valid NSWindow pointer that lives as long
        // as the window; we only borrow it on the main thread.
        let ns_window: &NSWindow = unsafe { &*(ptr as *const NSWindow) };
        let identifier = NSString::from_str("riochat-compact-toolbar");
        let toolbar = NSToolbar::initWithIdentifier(mtm.alloc::<NSToolbar>(), &identifier);
        ns_window.setToolbar(Some(&toolbar));
        ns_window.setToolbarStyle(NSWindowToolbarStyle::UnifiedCompact);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn layout_geometry() {
        let (strip, content) = compute_layout(1280.0, 840.0);
        assert_eq!(strip, (0.0, 0.0, 1280.0, 38.0));
        assert_eq!(content, (0.0, 38.0, 1280.0, 802.0));
        let (strip, content) = compute_layout(100.0, 20.0);
        assert_eq!(strip.3, 20.0);
        assert_eq!(content.3, 0.0);
        let (_, content) = compute_layout(-5.0, 0.0);
        assert_eq!((content.2, content.3), (0.0, 0.0));
    }

    fn allowed(url: &str, debug: bool) -> bool {
        navigation_allowed(&Url::parse(url).unwrap(), debug)
    }

    #[test]
    fn navigation_containment() {
        for u in [
            "tauri://localhost/chat",
            "about:blank",
            "blob:https://tauri.localhost/1234",
            "http://tauri.localhost/chat",
            "https://tauri.localhost/chat",
        ] {
            assert!(allowed(u, false), "{u}");
        }
        for u in [
            "https://example.com/",
            "http://localhost:3000/",
            "http://127.0.0.1:3000/",
            "file:///etc/passwd",
            "javascript:alert(1)",
            "data:text/html,hi",
            "https://tauri.localhost.evil.com/",
            "ftp://tauri.localhost/",
        ] {
            assert!(!allowed(u, false), "{u}");
        }
        assert!(allowed("http://localhost:3000/chat", true));
        assert!(allowed("https://127.0.0.1:3000/chat", true));
        assert!(!allowed("https://example.com/", true));
    }

    #[test]
    fn external_urls() {
        assert!(is_external_web_url(&Url::parse("https://a.b").unwrap()));
        assert!(is_external_web_url(&Url::parse("http://a.b").unwrap()));
        assert!(!is_external_web_url(&Url::parse("file:///x").unwrap()));
        assert!(!is_external_web_url(&Url::parse("mailto:a@b").unwrap()));
    }
}
