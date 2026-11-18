//! The old Strelka's tray icon starts at logon from the user's own Run key
//! (`tray/sys_windows.go` of the old repository, strelka-sing-box: value "sing-box
//! tray"). The daemon runs as SYSTEM and sees its own HKCU, not the user's,
//! so taking that entry off is the window's part of retiring the old
//! Strelka; the daemon stops its programs and its scheduled task
//! (legacy.retire). Only the entry goes: the old files stay where they are.

#[cfg(windows)]
mod imp {
    use windows::core::{w, PCWSTR};
    use windows::Win32::Foundation::{ERROR_FILE_NOT_FOUND, ERROR_MORE_DATA, ERROR_SUCCESS};
    use windows::Win32::System::Registry::{
        RegDeleteKeyValueW, RegGetValueW, HKEY_CURRENT_USER, RRF_RT_REG_SZ,
    };

    const RUN_KEY: PCWSTR = w!(r"Software\Microsoft\Windows\CurrentVersion\Run");
    const VALUE: PCWSTR = w!("sing-box tray");

    pub fn read() -> Option<String> {
        let mut buf = vec![0u16; 1024];
        loop {
            let mut size = (buf.len() * 2) as u32;
            // SAFETY: the buffer and its size in bytes describe the same
            // memory, and the strings are static and NUL-terminated.
            let err = unsafe {
                RegGetValueW(
                    HKEY_CURRENT_USER,
                    RUN_KEY,
                    VALUE,
                    RRF_RT_REG_SZ,
                    None,
                    Some(buf.as_mut_ptr().cast()),
                    Some(&mut size),
                )
            };
            if err == ERROR_MORE_DATA {
                buf.resize(size as usize / 2 + 1, 0);
                continue;
            }
            if err != ERROR_SUCCESS {
                return None;
            }
            let len = buf.iter().position(|&c| c == 0).unwrap_or(buf.len());
            return Some(String::from_utf16_lossy(&buf[..len]));
        }
    }

    pub fn remove() -> Result<bool, String> {
        // SAFETY: static NUL-terminated strings.
        let err = unsafe { RegDeleteKeyValueW(HKEY_CURRENT_USER, RUN_KEY, VALUE) };
        match err {
            e if e == ERROR_SUCCESS => Ok(true),
            e if e == ERROR_FILE_NOT_FOUND => Ok(false),
            e => Err(format!("код {}", e.0)),
        }
    }
}

#[cfg(not(windows))]
mod imp {
    pub fn read() -> Option<String> {
        None
    }

    pub fn remove() -> Result<bool, String> {
        Ok(false)
    }
}

/// The command line the old tray is started with at logon, or nothing.
#[tauri::command]
pub fn legacy_tray_autostart() -> Option<String> {
    imp::read()
}

/// Takes the old tray's entry off; false when there was none.
#[tauri::command]
pub fn legacy_tray_autostart_off() -> Result<bool, String> {
    imp::remove()
}
