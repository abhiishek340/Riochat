//! macOS menu bar. A custom menu replaces the framework default, whose Close
//! Window shortcut would hide the whole tab container.

use tauri::menu::{
    AboutMetadataBuilder, Menu, MenuItemBuilder, PredefinedMenuItem, SubmenuBuilder, HELP_SUBMENU_ID, WINDOW_SUBMENU_ID,
};
use tauri::{AppHandle, Wry};

pub const CLOSE_TAB_ID: &str = "close-tab";
pub const CLOSE_WINDOW_ID: &str = "close-window";

/// What the File menu offers for a given number of tabs.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct FileMenuPlan {
    /// Label of the Cmd+W item.
    pub primary_label: &'static str,
    /// Whether the second "Close Window" (Cmd+Shift+W) item exists.
    pub has_close_window: bool,
}

pub fn file_menu_plan(tab_count: usize) -> FileMenuPlan {
    if tab_count > 1 {
        FileMenuPlan {
            primary_label: "Close Tab",
            has_close_window: true,
        }
    } else {
        FileMenuPlan {
            primary_label: "Close Window",
            has_close_window: false,
        }
    }
}

/// What the Cmd+W item does: with one tab it closes (hides) the window rather
/// than the last tab, which would delete the token and stop the sidecar.
pub fn close_primary_closes_tab(tab_count: usize) -> bool {
    tab_count > 1
}

pub fn build(app: &AppHandle, tab_count: usize) -> tauri::Result<Menu<Wry>> {
    let plan = file_menu_plan(tab_count);
    let about = AboutMetadataBuilder::new()
        .name(Some("Riochat"))
        .version(Some(app.package_info().version.to_string()))
        .copyright(Some("(c) Riochat"))
        .build();

    let app_menu = SubmenuBuilder::new(app, "Riochat")
        .item(&PredefinedMenuItem::about(app, None, Some(about))?)
        .separator()
        .services()
        .separator()
        .hide()
        .hide_others()
        .separator()
        .quit()
        .build()?;

    let close_primary = MenuItemBuilder::with_id(CLOSE_TAB_ID, plan.primary_label)
        .accelerator("CmdOrCtrl+W")
        .build(app)?;
    let mut file = SubmenuBuilder::new(app, "File").item(&close_primary);
    if plan.has_close_window {
        let close_window = MenuItemBuilder::with_id(CLOSE_WINDOW_ID, "Close Window")
            .accelerator("CmdOrCtrl+Shift+W")
            .build(app)?;
        file = file.item(&close_window);
    }
    let file = file.build()?;

    let edit = SubmenuBuilder::new(app, "Edit")
        .undo()
        .redo()
        .separator()
        .cut()
        .copy()
        .paste()
        .select_all()
        .build()?;
    let view = SubmenuBuilder::new(app, "View").fullscreen().build()?;
    let window = SubmenuBuilder::with_id(app, WINDOW_SUBMENU_ID, "Window")
        .minimize()
        .maximize()
        .build()?;
    let help = SubmenuBuilder::with_id(app, HELP_SUBMENU_ID, "Help").build()?;

    Menu::with_items(app, &[&app_menu, &file, &edit, &view, &window, &help])
}

/// Installs the menu and patches the native Cmd+Shift+W item. Runs on the main
/// thread; failures are logged only.
pub fn install(app: &AppHandle, tab_count: usize) {
    let handle = app.clone();
    let _ = app.run_on_main_thread(move || match build(&handle, tab_count) {
        Ok(menu) => {
            if let Err(e) = handle.set_menu(menu) {
                log::warn!("cannot install the menu: {e}");
                return;
            }
            patch_shifted_close_window();
        }
        Err(e) => log::warn!("cannot build the menu: {e}"),
    });
}

/// The platform maps Cmd+Shift+W to the shifted key equivalent "W". The
/// installed native item is rewritten so the shortcut really fires.
fn patch_shifted_close_window() {
    use objc2::MainThreadMarker;
    use objc2_app_kit::{NSApplication, NSEventModifierFlags};
    use objc2_foundation::NSString;

    let Some(mtm) = MainThreadMarker::new() else { return };
    let ns_app = NSApplication::sharedApplication(mtm);
    let Some(main_menu) = ns_app.mainMenu() else { return };
    for top in main_menu.itemArray().iter() {
        let Some(sub) = top.submenu() else { continue };
        for item in sub.itemArray().iter() {
            let mask = item.keyEquivalentModifierMask();
            if item.title().to_string() == "Close Window" && mask.contains(NSEventModifierFlags::Shift) {
                item.setKeyEquivalent(&NSString::from_str("W"));
                item.setKeyEquivalentModifierMask(mask.difference(NSEventModifierFlags::Shift));
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn file_menu_follows_tab_count() {
        let one = file_menu_plan(1);
        assert_eq!(one.primary_label, "Close Window");
        assert!(!one.has_close_window);
        assert_eq!(file_menu_plan(0), one);
        let many = file_menu_plan(2);
        assert_eq!(many.primary_label, "Close Tab");
        assert!(many.has_close_window);
        assert!(!close_primary_closes_tab(1));
        assert!(close_primary_closes_tab(3));
    }
}
