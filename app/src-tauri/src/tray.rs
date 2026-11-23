//! The tray icon and the one window behind it.
//!
//! The tray belongs to the app, not to the daemon: a service runs in session
//! zero and cannot draw. Which icon and which words to show is decided by the
//! page from the daemon's state and passed here with `tray_set`; this module
//! only knows how to put them on screen.

use std::sync::Mutex;

use serde::Deserialize;
use tauri::image::Image;
use tauri::menu::{CheckMenuItem, Menu, MenuItem, Submenu};
use tauri::tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent};
use tauri::{AppHandle, Emitter, Manager, State, Wry};
use tauri_plugin_window_state::{AppHandleExt, StateFlags};

const TRAY_ID: &str = "main";
const WINDOW: &str = "main";

/// Tells the page that "turn on / turn off" was chosen in the menu. The page
/// knows what the VPN is doing and calls the daemon itself.
const EVENT_TOGGLE: &str = "tray://toggle";

/// Tells the page that a server was chosen in the "Сервер" submenu; the
/// payload is its id.
const EVENT_SERVER: &str = "tray://server";
/// Menu ids of servers carry this prefix before the server's id.
const SERVER_PREFIX: &str = "server:";

/// Tells the page whether the window is on screen. A hidden WebView still
/// reports itself as visible, and the page should not keep asking the daemon
/// for live figures nobody sees.
const EVENT_SHOWN: &str = "window://shown";

// Until the page has loaded and sent its own texts, the menu needs words.
// The dictionary (app/src/lib/i18n/ru.ts) replaces all of them on tray_set.
const LABEL_OPEN: &str = "Открыть";
const LABEL_TOGGLE: &str = "Включить VPN";
const LABEL_SERVER: &str = "Сервер";
const LABEL_QUIT: &str = "Выход";
const TOOLTIP: &str = "Astra";

// Drawn by app/scripts/icons.mjs.
const ICONS: &[(&str, &[u8])] = &[
    ("on", include_bytes!("../icons/tray/on.png")),
    ("off", include_bytes!("../icons/tray/off.png")),
    ("connecting", include_bytes!("../icons/tray/connecting.png")),
    ("connecting-dim", include_bytes!("../icons/tray/connecting-dim.png")),
    ("problem", include_bytes!("../icons/tray/problem.png")),
    ("down", include_bytes!("../icons/tray/down.png")),
    ("blocked", include_bytes!("../icons/tray/blocked.png")),
];

fn icon(name: &str) -> Option<Image<'static>> {
    let bytes = ICONS.iter().find(|(known, _)| *known == name)?.1;
    Image::from_bytes(bytes).ok()
}

/// The menu items whose text changes with the state.
pub struct Items {
    open: MenuItem<Wry>,
    toggle: MenuItem<Wry>,
    server: Submenu<Wry>,
    quit: MenuItem<Wry>,
    /// What the server submenu shows now, so it is rebuilt only on change.
    servers: Mutex<Vec<ServerItem>>,
}

/// Takes the icon off the taskbar and puts it back. The updater takes it
/// down before the installer starts: the process then ends without the
/// cleanup that would remove it, and Windows would keep a dead icon until
/// the mouse passes over it.
pub fn set_visible(app: &AppHandle, visible: bool) {
    if let Some(tray) = app.tray_by_id(TRAY_ID) {
        let _ = tray.set_visible(visible);
    }
}

pub fn build(app: &AppHandle) -> tauri::Result<()> {
    let open = MenuItem::with_id(app, "open", LABEL_OPEN, true, None::<&str>)?;
    // Disabled until the page says the daemon is there to be told.
    let toggle = MenuItem::with_id(app, "toggle", LABEL_TOGGLE, false, None::<&str>)?;
    let server = Submenu::with_id(app, "server", LABEL_SERVER, false)?;
    let quit = MenuItem::with_id(app, "quit", LABEL_QUIT, true, None::<&str>)?;
    let menu = Menu::with_items(app, &[&open, &toggle, &server, &quit])?;

    let mut tray = TrayIconBuilder::with_id(TRAY_ID)
        .tooltip(TOOLTIP)
        .menu(&menu)
        // Left click is for the window; the menu is on the right button.
        .show_menu_on_left_click(false)
        .on_menu_event(|app, event| match event.id().as_ref() {
            "open" => show_window(app),
            "toggle" => {
                let _ = app.emit(EVENT_TOGGLE, ());
            }
            // The only way to quit: closing the window merely hides it.
            "quit" => app.exit(0),
            id => {
                if let Some(server) = id.strip_prefix(SERVER_PREFIX) {
                    let _ = app.emit(EVENT_SERVER, server.to_string());
                }
            }
        })
        .on_tray_icon_event(|tray, event| {
            if let TrayIconEvent::Click {
                button: MouseButton::Left,
                button_state: MouseButtonState::Up,
                ..
            } = event
            {
                toggle_window(tray.app_handle());
            }
        });
    if let Some(image) = icon("off") {
        tray = tray.icon(image);
    }
    tray.build(app)?;

    app.manage(Items { open, toggle, server, quit, servers: Mutex::new(Vec::new()) });
    Ok(())
}

/// Lets the page bring the main window up: a toast's "Открыть" is pressed
/// in another window, which cannot show this one by itself.
#[tauri::command]
pub fn main_show(app: AppHandle) {
    show_window(&app);
}

pub fn show_window(app: &AppHandle) {
    if let Some(window) = app.get_webview_window(WINDOW) {
        let _ = window.unminimize();
        let _ = window.show();
        let _ = window.set_focus();
        let _ = app.emit(EVENT_SHOWN, true);
    }
}

/// What is remembered about the window between runs. Not VISIBLE: whether
/// the window shows at start is decided by --hidden, not by the last run.
pub fn remembered() -> StateFlags {
    StateFlags::SIZE | StateFlags::POSITION | StateFlags::MAXIMIZED
}

pub fn hide_window(app: &AppHandle) {
    if let Some(window) = app.get_webview_window(WINDOW) {
        // The plugin writes its file only when the app exits, and this app
        // usually ends with the user's session, without a chance to exit.
        // Hiding is the moment the user is done with the window: save now.
        let _ = app.save_window_state(remembered());
        let _ = window.hide();
        let _ = app.emit(EVENT_SHOWN, false);
    }
}

fn toggle_window(app: &AppHandle) {
    let Some(window) = app.get_webview_window(WINDOW) else {
        return;
    };
    let on_screen = window.is_visible().unwrap_or(false) && !window.is_minimized().unwrap_or(false);
    if on_screen {
        hide_window(app);
    } else {
        show_window(app);
    }
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Labels {
    open: String,
    toggle: String,
    server: String,
    no_favorites: String,
    quit: String,
}

/// One line of the "Сервер" submenu: a favourite, ticked when selected.
#[derive(Deserialize, Clone, PartialEq)]
pub struct ServerItem {
    id: String,
    label: String,
    checked: bool,
}

/// Called by the page whenever what the tray shows has changed.
#[tauri::command]
pub fn tray_set(
    app: AppHandle,
    items: State<'_, Items>,
    icon: String,
    tooltip: String,
    labels: Labels,
    can_toggle: bool,
    servers: Vec<ServerItem>,
) {
    if let Some(tray) = app.tray_by_id(TRAY_ID) {
        if let Some(image) = self::icon(&icon) {
            let _ = tray.set_icon(Some(image));
        }
        let _ = tray.set_tooltip(Some(tooltip));
    }
    let _ = items.open.set_text(labels.open);
    let _ = items.toggle.set_text(labels.toggle);
    let _ = items.toggle.set_enabled(can_toggle);
    let _ = items.server.set_text(labels.server);
    let _ = items.server.set_enabled(can_toggle);
    let _ = items.quit.set_text(labels.quit);
    set_servers(&app, &items, servers, &labels.no_favorites);
}

/// Rebuilds the submenu when its lines changed. Menus have no "replace all",
/// so the old lines go and the new ones come.
fn set_servers(app: &AppHandle, items: &Items, servers: Vec<ServerItem>, empty: &str) {
    let Ok(mut shown) = items.servers.lock() else {
        return;
    };
    let fresh = items.server.items().map(|list| list.is_empty()).unwrap_or(true);
    if *shown == servers && !fresh {
        return;
    }
    if let Ok(old) = items.server.items() {
        for item in old {
            let _ = items.server.remove(&item);
        }
    }
    if servers.is_empty() {
        if let Ok(line) = MenuItem::with_id(app, "server-none", empty, false, None::<&str>) {
            let _ = items.server.append(&line);
        }
    }
    for s in &servers {
        let id = format!("{SERVER_PREFIX}{}", s.id);
        if let Ok(line) = CheckMenuItem::with_id(app, id, &s.label, true, s.checked, None::<&str>) {
            let _ = items.server.append(&line);
        }
    }
    *shown = servers;
}
