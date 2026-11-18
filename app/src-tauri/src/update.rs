//! Updates from GitHub releases through tauri-plugin-updater, driven by the
//! page (lib/update.svelte.ts) with three commands: check, download,
//! install. The key and the address are in tauri.conf.json.
//!
//! The plugin's own commands are not used. Its default hook before the
//! installer starts is `cleanup_before_exit`, which destroys the windows and
//! the tray icon, and on Windows it runs before ShellExecute - that is,
//! before the UAC prompt is answered. A declined prompt then left a process
//! with no window and no tray icon (seen in the stage 08b check), and
//! single-instance handed every new start of Strelka to that process. Here
//! the hook only takes the tray icon down, so that no ghost icon outlives
//! the process; a failed start puts it back, and the page says what went
//! wrong in the window that is still there.

use std::sync::Mutex;
use std::time::Duration;

use serde::Serialize;
use tauri::{ipc::Channel, AppHandle, State};
use tauri_plugin_updater::{Update, UpdaterExt};

use crate::tray;

const CHECK_TIMEOUT: Duration = Duration::from_secs(30);

/// The update on offer and, once downloaded and verified, its package.
#[derive(Default)]
pub struct Pending {
    update: Mutex<Option<Update>>,
    package: Mutex<Option<Vec<u8>>>,
}

/// What the dialog shows; `date` is RFC 3339 as latest.json has it.
#[derive(Serialize)]
pub struct Offer {
    version: String,
    date: Option<String>,
    notes: String,
}

#[derive(Clone, Serialize)]
#[serde(tag = "event", rename_all = "lowercase")]
pub enum Progress {
    /// Bytes so far, and the size when the server said it.
    Chunk { got: u64, total: Option<u64> },
    /// All bytes are in; the signature is checked next.
    Downloaded,
}

#[tauri::command]
pub async fn update_check(
    app: AppHandle,
    pending: State<'_, Pending>,
) -> Result<Option<Offer>, String> {
    let hook = app.clone();
    let updater = app
        .updater_builder()
        .timeout(CHECK_TIMEOUT)
        .on_before_exit(move || tray::set_visible(&hook, false))
        .build()
        .map_err(|e| e.to_string())?;
    let found = updater.check().await.map_err(|e| e.to_string())?;
    let offer = found.as_ref().map(|u| Offer {
        version: u.version.clone(),
        date: u
            .raw_json
            .get("pub_date")
            .and_then(|v| v.as_str())
            .map(str::to_owned),
        notes: u.body.as_deref().unwrap_or_default().trim().to_owned(),
    });
    *pending.update.lock().unwrap() = found;
    *pending.package.lock().unwrap() = None;
    Ok(offer)
}

/// Downloads the package and checks its signature; progress goes to the
/// page over the channel.
#[tauri::command]
pub async fn update_download(
    pending: State<'_, Pending>,
    progress: Channel<Progress>,
) -> Result<(), String> {
    let update = pending
        .update
        .lock()
        .unwrap()
        .clone()
        .ok_or("nothing to download")?;
    let mut got = 0u64;
    let package = update
        .download(
            |chunk, total| {
                got += chunk as u64;
                let _ = progress.send(Progress::Chunk { got, total });
            },
            || {
                let _ = progress.send(Progress::Downloaded);
            },
        )
        .await
        .map_err(|e| e.to_string())?;
    *pending.package.lock().unwrap() = Some(package);
    Ok(())
}

/// Starts the installer. On success the plugin ends this process and the
/// installer starts Strelka again; only a failure comes back here - most
/// often a declined UAC prompt.
#[tauri::command]
pub async fn update_install(app: AppHandle, pending: State<'_, Pending>) -> Result<(), String> {
    let update = pending
        .update
        .lock()
        .unwrap()
        .clone()
        .ok_or("nothing to install")?;
    let package = pending
        .package
        .lock()
        .unwrap()
        .take()
        .ok_or("nothing downloaded")?;
    // ShellExecute waits for the UAC prompt; the window must keep drawing.
    let result = tauri::async_runtime::spawn_blocking(move || update.install(package))
        .await
        .map_err(|e| e.to_string())?;
    tray::set_visible(&app, true);
    result.map_err(|e| e.to_string())
}
