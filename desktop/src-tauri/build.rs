fn main() {
    tauri_build::try_build(
        tauri_build::Attributes::new().app_manifest(tauri_build::AppManifest::new().commands(&[
            "tabs_list",
            "tabs_open",
            "tabs_activate",
            "tabs_close",
            "tabs_move",
            "get_distribution",
            "check_update_notice",
            "check_update",
            "get_network_proxy",
            "set_network_proxy",
            "get_server",
            "set_remote_server",
            "set_local_server",
            "leave_server",
            "local_sign_in",
            "store_session",
            "clear_session",
            "refresh_session",
            "start_oauth_loopback",
            "stop_oauth_loopback",
        ])),
    )
    .expect("failed to run the tauri build step");
}
