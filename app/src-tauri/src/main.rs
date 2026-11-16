// No console window next to the app in release builds.
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

//! The shell of Strelka's window. It is deliberately thin: a window, a tray
//! icon, one running copy, start at login, and a bridge that carries lines of
//! JSON between the page and the daemon. What those lines mean is the page's
//! business (app/src/lib), so a new daemon method needs no change here.

mod bridge;
mod tray;

use tauri::{Manager, WindowEvent};
use tauri_plugin_autostart::MacosLauncher;

/// Started at login, the app stays in the tray until asked for.
const HIDDEN_FLAG: &str = "--hidden";

fn main() {
    let hidden = std::env::args().any(|arg| arg == HIDDEN_FLAG);

    tauri::Builder::default()
        // Must be the first plugin: a second copy hands over and exits
        // before anything else of it starts.
        .plugin(tauri_plugin_single_instance::init(|app, _args, _cwd| {
            tray::show_window(app);
        }))
        .plugin(tauri_plugin_autostart::init(
            MacosLauncher::LaunchAgent,
            Some(vec![HIDDEN_FLAG]),
        ))
        // Shows the report for support in the file manager.
        .plugin(tauri_plugin_opener::init())
        .plugin(
            tauri_plugin_window_state::Builder::default()
                .with_state_flags(tray::remembered())
                .build(),
        )
        .invoke_handler(tauri::generate_handler![
            bridge::daemon_send,
            bridge::daemon_status,
            bridge::daemon_retry,
            tray::tray_set,
        ])
        .setup(move |app| {
            tray::build(app.handle())?;
            app.manage(bridge::start(app.handle().clone()));
            // The window exists from the start, only hidden: opening it from
            // the tray is then a matter of showing it, not of loading a page.
            if !hidden {
                tray::show_window(app.handle());
            }
            Ok(())
        })
        .on_window_event(|window, event| {
            // Closing hides to the tray; quitting is the tray menu's job.
            if let WindowEvent::CloseRequested { api, .. } = event {
                api.prevent_close();
                tray::hide_window(window.app_handle());
            }
        })
        .run(tauri::generate_context!())
        .expect("failed to start the Strelka window");
}
