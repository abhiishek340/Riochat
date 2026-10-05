//! Riochat desktop shell.
//!
//! A thin native host around the exported web client: a tab strip, one content
//! webview per server, an optional bundled server ("local mode"), credential
//! storage in the OS keychain, tray, menu, downloads, proxy and policy.

pub mod bootstrap;
pub mod commands;
pub mod creds;
pub mod distribution;
pub mod downloads;
pub mod error;
pub mod fsutil;
#[cfg(target_os = "macos")]
pub mod menu;
pub mod oauth;
pub mod origin;
pub mod policy;
pub mod portable;
pub mod proxy;
pub mod server;
pub mod session;
pub mod shell;
pub mod sidecar;
pub mod tabs;
pub mod tray;
pub mod updates;
pub mod window;

use creds::{KeyringStore, SecretStore};
use shell::{AppConfig, Shell};
use std::sync::Arc;
use tauri::{Manager, RunEvent, WindowEvent};

/// Installs the process-wide TLS provider. Must run before any HTTPS client
/// is created.
fn install_tls() {
    let _ = rustls::crypto::ring::default_provider().install_default();
}

/// Reads the saved proxy password, only for a manual proxy with a user name.
fn load_proxy_password(store: &dyn SecretStore, settings: &proxy::settings::Settings) -> Option<String> {
    if settings.mode != proxy::settings::Mode::Manual || settings.username.trim().is_empty() {
        return None;
    }
    match store.get(creds::PROXY_PASSWORD_ACCOUNT) {
        Ok(p) => p,
        Err(e) => {
            log::warn!("cannot read the proxy password: {e}");
            None
        }
    }
}

pub fn run() {
    let _ = env_logger::Builder::from_env(env_logger::Env::default().default_filter_or("info")).try_init();
    install_tls();

    let mut context = tauri::generate_context!();
    let version = context.package_info().version.to_string();
    let first_endpoint = updates::first_endpoint(context.config());
    let boot = bootstrap::prepare(&context.config().identifier, first_endpoint.as_deref(), &version);
    // a portable copy gets its own credential-store service and instance key
    context.config_mut().identifier = boot.identifier.clone();

    let app = tauri::Builder::default()
        .plugin(tauri_plugin_single_instance::init(|app, _args, _cwd| {
            window::show_main(app);
        }))
        .plugin(tauri_plugin_updater::Builder::new().build())
        .plugin(tauri_plugin_process::init())
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_notification::init())
        .plugin(tauri_plugin_shell::init())
        .invoke_handler(tauri::generate_handler![
            commands::tabs_list,
            commands::tabs_open,
            commands::tabs_activate,
            commands::tabs_close,
            commands::tabs_move,
            commands::get_distribution,
            commands::check_update_notice,
            commands::check_update,
            commands::get_network_proxy,
            commands::set_network_proxy,
            commands::get_server,
            commands::set_remote_server,
            commands::set_local_server,
            commands::leave_server,
            commands::local_sign_in,
            commands::store_session,
            commands::clear_session,
            commands::refresh_session,
            commands::start_oauth_loopback,
            commands::stop_oauth_loopback,
        ])
        .on_window_event(|win, event| {
            if win.label() != window::MAIN_LABEL {
                return;
            }
            match event {
                // macOS and Windows keep running behind the tray; Linux quits
                #[cfg(any(target_os = "macos", windows))]
                WindowEvent::CloseRequested { api, .. } => {
                    api.prevent_close();
                    let _ = win.hide();
                }
                WindowEvent::Resized(_) | WindowEvent::ScaleFactorChanged { .. } => {
                    if let Some(shell) = win.app_handle().try_state::<Shell>() {
                        shell.relayout();
                    }
                }
                _ => {}
            }
        })
        .setup(move |app| {
            let handle = app.handle().clone();
            let (config_dir, data_dir) = match &boot.portable {
                Some(layout) => {
                    if let Err(e) = portable::reset_tmp(layout) {
                        log::warn!("cannot reset the portable temp folder: {e}");
                    }
                    (layout.data.clone(), layout.data.clone())
                }
                None => (app.path().app_config_dir()?, app.path().app_data_dir()?),
            };
            std::fs::create_dir_all(&config_dir)?;

            // proxy state is read once, before any webview or sidecar exists
            let store: Arc<dyn SecretStore> = Arc::new(KeyringStore::new(boot.identifier.clone()));
            let saved = proxy::settings::load(&config_dir.join("network.json"));
            let password = load_proxy_password(store.as_ref(), &saved);
            let proxy_state = proxy::State::capture(
                &boot.policy,
                saved,
                password,
                proxy::os::read_environment(),
                proxy::os::read_os(),
                cfg!(windows),
            );
            log::info!("network proxy source: {:?}", proxy_state.effective.source);

            if boot.update_mode == distribution::UpdateMode::Auto {
                // without it the app still runs, it just cannot install updates
                if let Err(e) = app.add_capability(updates::RUNTIME_CAPABILITY) {
                    log::error!("cannot grant the updater capability: {e}");
                }
            }

            let shell = Shell::new(
                handle.clone(),
                AppConfig {
                    identifier: boot.identifier.clone(),
                    kind: boot.kind,
                    update_mode: boot.update_mode,
                    releases_url: boot.releases_url.clone(),
                    policy: boot.policy.clone(),
                    config_dir,
                    data_dir,
                    portable: boot.portable.clone(),
                },
                store,
                proxy_state,
            );
            app.manage(shell.clone());
            shell.start()?;
            tray::create(&handle)?;
            Ok(())
        });

    #[cfg(target_os = "macos")]
    let app = app.on_menu_event(|app, event| {
        let Some(shell) = app.try_state::<Shell>() else { return };
        match event.id().as_ref() {
            menu::CLOSE_TAB_ID => {
                if menu::close_primary_closes_tab(shell.tab_count()) {
                    let shell = shell.inner().clone();
                    // off the main thread; failures are logged only
                    tauri::async_runtime::spawn(async move {
                        if let Err(e) = shell.close_active_tab() {
                            log::warn!("cannot close the tab: {e}");
                        }
                    });
                } else if let Some(w) = app.get_window(window::MAIN_LABEL) {
                    let _ = w.hide();
                }
            }
            menu::CLOSE_WINDOW_ID => {
                if let Some(w) = app.get_window(window::MAIN_LABEL) {
                    let _ = w.hide();
                }
            }
            _ => {}
        }
    });

    app.build(context)
        .expect("failed to build the Riochat desktop application")
        .run(|app, event| match event {
            RunEvent::Exit => {
                if let Some(shell) = app.try_state::<Shell>() {
                    // synchronous best-effort kill that never waits on locks
                    shell.sidecar().kill_now();
                }
            }
            #[cfg(target_os = "macos")]
            RunEvent::Reopen { .. } => window::show_main(app),
            _ => {}
        });
}
