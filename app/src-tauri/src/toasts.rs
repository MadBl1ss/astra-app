//! The toast window: a frameless, transparent window over all others, in the
//! bottom right corner of the main monitor's work area, exactly as tall as
//! its cards. It must never take the focus: a toast that appears while the
//! user types would eat their keystrokes.
//!
//! The page in it only draws what the main page sends (app/src/lib/shell/
//! ToastWindow.svelte); deciding what to show is the main page's business.

use tauri::{AppHandle, Manager, WebviewUrl, WebviewWindow, WebviewWindowBuilder};

pub const LABEL: &str = "toasts";

/// Logical pixels, the stack's width with room for the cards' shadow.
const WIDTH: f64 = 360.0;

/// Whether the window could be made; if not, toasts go to the system.
pub struct Native(pub bool);

/// Makes the window, hidden. Its failure is not the app's: the page falls
/// back to the system's notifications.
pub fn build(app: &AppHandle) -> bool {
    let mut builder = WebviewWindowBuilder::new(app, LABEL, WebviewUrl::App("index.html?window=toasts".into()));
    // Both windows live in one WebView2 browser process, which refuses a
    // second webview asking for other browser arguments than the first.
    let main = app.config().app.windows.iter().find(|w| w.label == "main");
    if let Some(args) = main.and_then(|w| w.additional_browser_args.as_deref()) {
        builder = builder.additional_browser_args(args);
    }
    let built = builder
        .title("Astra")
        .decorations(false)
        .transparent(true)
        // Windows 11 would outline the transparent rectangle; the cards
        // draw their own shadow.
        .shadow(false)
        .always_on_top(true)
        .skip_taskbar(true)
        .resizable(false)
        .focused(false)
        .focusable(false)
        .visible(false)
        .inner_size(WIDTH, 120.0)
        .build();
    match built {
        Ok(window) => {
            no_activate(&window);
            true
        }
        Err(err) => {
            eprintln!("toast window: {err}");
            false
        }
    }
}

#[tauri::command]
pub fn toasts_native(native: tauri::State<'_, Native>) -> bool {
    native.0
}

/// Puts the window in the corner with the given height (logical pixels) and
/// shows it, or hides it for 0. The place is worked out every time: the
/// main monitor and its scale may have changed since the last toast.
#[tauri::command]
pub fn toasts_place(app: AppHandle, height: f64) {
    let Some(window) = app.get_webview_window(LABEL) else {
        return;
    };
    if height <= 0.0 {
        hide(&window);
        return;
    }
    let Ok(Some(monitor)) = app.primary_monitor() else {
        return;
    };
    let scale = monitor.scale_factor();
    let area = monitor.work_area();
    let w = (WIDTH * scale).round() as i32;
    let h = (height * scale).round() as i32;
    let x = area.position.x + area.size.width as i32 - w;
    let y = area.position.y + area.size.height as i32 - h;
    show_at(&window, x, y, w, h);
}

/// Whether a full-screen program (a game, a presentation, a film) is in
/// front: then toasts that are not critical wait.
#[tauri::command]
pub fn screen_busy() -> bool {
    #[cfg(windows)]
    {
        use windows::Win32::UI::Shell::{
            SHQueryUserNotificationState, QUNS_BUSY, QUNS_PRESENTATION_MODE, QUNS_RUNNING_D3D_FULL_SCREEN,
        };
        // SAFETY: a plain query with no arguments.
        match unsafe { SHQueryUserNotificationState() } {
            Ok(state) => state == QUNS_BUSY || state == QUNS_RUNNING_D3D_FULL_SCREEN || state == QUNS_PRESENTATION_MODE,
            Err(_) => false,
        }
    }
    #[cfg(not(windows))]
    false
}

#[cfg(windows)]
fn no_activate(window: &WebviewWindow) {
    use windows::Win32::UI::WindowsAndMessaging::{
        GetWindowLongPtrW, SetWindowLongPtrW, GWL_EXSTYLE, WS_EX_APPWINDOW, WS_EX_NOACTIVATE, WS_EX_TOOLWINDOW,
    };
    // focusable(false) already gives WS_EX_NOACTIVATE; TOOLWINDOW keeps the
    // window out of Alt+Tab, which APPWINDOW (set by tao) would undo. Both
    // are set here so that neither depends on how a future Tauri spells it.
    let Ok(hwnd) = window.hwnd() else {
        return;
    };
    // SAFETY: the handle belongs to a live window of this process.
    unsafe {
        let ex = GetWindowLongPtrW(hwnd, GWL_EXSTYLE);
        let ex = (ex | (WS_EX_NOACTIVATE.0 | WS_EX_TOOLWINDOW.0) as isize) & !(WS_EX_APPWINDOW.0 as isize);
        SetWindowLongPtrW(hwnd, GWL_EXSTYLE, ex);
    }
}

#[cfg(not(windows))]
fn no_activate(_: &WebviewWindow) {}

/// Shows, moves and sizes the window in one call that does not activate it.
/// Tauri's show() would: it is ShowWindow(SW_SHOW).
#[cfg(windows)]
fn show_at(window: &WebviewWindow, x: i32, y: i32, w: i32, h: i32) {
    use windows::Win32::UI::WindowsAndMessaging::{
        GetForegroundWindow, SetWindowPos, HWND_TOPMOST, SWP_NOACTIVATE, SWP_SHOWWINDOW,
    };
    let Ok(hwnd) = window.hwnd() else {
        return;
    };
    // SAFETY: as above; GetForegroundWindow has no preconditions.
    let before = unsafe { GetForegroundWindow() }.0 as isize;
    unsafe {
        let _ = SetWindowPos(hwnd, Some(HWND_TOPMOST), x, y, w, h, SWP_NOACTIVATE | SWP_SHOWWINDOW);
    }
    if cfg!(debug_assertions) {
        // A check without eyes: whatever had the focus must still have it.
        std::thread::spawn(move || {
            std::thread::sleep(std::time::Duration::from_millis(300));
            let after = unsafe { GetForegroundWindow() }.0 as isize;
            if after == before {
                eprintln!("toasts: shown {w}x{h} at {x},{y}; focus stayed on {before:#x}");
            } else {
                eprintln!("toasts: FOCUS MOVED from {before:#x} to {after:#x} when shown");
            }
        });
    }
}

#[cfg(not(windows))]
fn show_at(window: &WebviewWindow, x: i32, y: i32, w: i32, h: i32) {
    use tauri::{PhysicalPosition, PhysicalSize};
    let _ = window.set_size(PhysicalSize::new(w as u32, h as u32));
    let _ = window.set_position(PhysicalPosition::new(x, y));
    let _ = window.show();
}

#[cfg(windows)]
fn hide(window: &WebviewWindow) {
    use windows::Win32::UI::WindowsAndMessaging::{ShowWindow, SW_HIDE};
    // Not window.hide(): Tauri does not know the window was shown, as it was
    // shown past it, and would skip hiding a window it thinks is hidden.
    if let Ok(hwnd) = window.hwnd() {
        // SAFETY: as above.
        unsafe {
            let _ = ShowWindow(hwnd, SW_HIDE);
        }
    }
}

#[cfg(not(windows))]
fn hide(window: &WebviewWindow) {
    let _ = window.hide();
}
