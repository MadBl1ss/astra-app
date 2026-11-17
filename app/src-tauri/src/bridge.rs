//! The bridge between the page and the daemon's channel.
//!
//! It knows the transport of the protocol (one JSON object per line over
//! a named pipe or a unix socket) and nothing of the protocol itself. Lines
//! from the page go to the daemon as they are, lines from the daemon go to
//! the page as `daemon://line` events, and matching answers to requests is
//! done in TypeScript. When the channel drops, the bridge reconnects with a
//! growing pause and reports each change as a `daemon://status` event.

use std::sync::{Arc, Mutex};
use std::time::Duration;

use serde::Serialize;
use tauri::{AppHandle, Emitter, State};
use tokio::io::{AsyncBufReadExt, AsyncRead, AsyncWrite, AsyncWriteExt, BufReader};
use tokio::sync::mpsc;
use tokio::time;

const EVENT_LINE: &str = "daemon://line";
const EVENT_STATUS: &str = "daemon://status";

/// The pause before the next attempt doubles from MIN up to MAX: a daemon
/// that is restarting is found at once, one that is gone is not hammered.
const PAUSE_MIN: Duration = Duration::from_millis(250);
const PAUSE_MAX: Duration = Duration::from_secs(5);

/// Overrides the channel, like strelkactl's --pipe.
const ENV_CHANNEL: &str = "STRELKA_PIPE";

#[derive(Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "lowercase")]
pub enum Status {
    /// The first attempt, or one the user asked for, is under way.
    Connecting,
    Connected,
    Disconnected,
}

enum Command {
    Line(String),
    Retry,
}

pub struct Bridge {
    commands: mpsc::UnboundedSender<Command>,
    status: Arc<Mutex<Status>>,
}

pub fn start(app: AppHandle) -> Bridge {
    let (commands, inbox) = mpsc::unbounded_channel();
    let status = Arc::new(Mutex::new(Status::Connecting));
    tauri::async_runtime::spawn(run(app, status.clone(), inbox));
    Bridge { commands, status }
}

/// Sends one line to the daemon. Fails at once when there is no connection:
/// the page then rejects the request instead of waiting for a timeout.
#[tauri::command]
pub fn daemon_send(bridge: State<'_, Bridge>, line: String) -> Result<(), &'static str> {
    if *bridge.status.lock().unwrap() != Status::Connected {
        return Err("disconnected");
    }
    bridge
        .commands
        .send(Command::Line(line))
        .map_err(|_| "disconnected")
}

/// The page asks once after it starts listening: the bridge may have
/// connected before the page existed, and events are not replayed.
#[tauri::command]
pub fn daemon_status(bridge: State<'_, Bridge>) -> Status {
    *bridge.status.lock().unwrap()
}

/// "Try now": cuts the current pause short.
#[tauri::command]
pub fn daemon_retry(bridge: State<'_, Bridge>) {
    let _ = bridge.commands.send(Command::Retry);
}

fn set_status(app: &AppHandle, shared: &Mutex<Status>, status: Status) {
    {
        let mut current = shared.lock().unwrap();
        if *current == status {
            return;
        }
        *current = status;
    }
    let _ = app.emit(EVENT_STATUS, status);
}

async fn run(app: AppHandle, status: Arc<Mutex<Status>>, mut inbox: mpsc::UnboundedReceiver<Command>) {
    let address = address();
    let mut pause = PAUSE_MIN;
    loop {
        if let Ok(stream) = connect(&address).await {
            set_status(&app, &status, Status::Connected);
            pause = PAUSE_MIN;
            if !pump(&app, stream, &mut inbox).await {
                return;
            }
        }
        set_status(&app, &status, Status::Disconnected);

        // Wait out the pause. Lines that were queued for the lost connection
        // are dropped: the page has already failed those requests.
        let sleep = time::sleep(pause);
        tokio::pin!(sleep);
        loop {
            tokio::select! {
                _ = &mut sleep => break,
                command = inbox.recv() => match command {
                    Some(Command::Retry) => {
                        set_status(&app, &status, Status::Connecting);
                        break;
                    }
                    Some(Command::Line(_)) => {}
                    None => return,
                },
            }
        }
        pause = (pause * 2).min(PAUSE_MAX);
    }
}

/// Carries lines both ways until the connection ends. Returns false when the
/// app itself is shutting down and there is nothing to reconnect for.
async fn pump<S>(app: &AppHandle, stream: S, inbox: &mut mpsc::UnboundedReceiver<Command>) -> bool
where
    S: AsyncRead + AsyncWrite,
{
    let (reader, mut writer) = tokio::io::split(stream);
    let mut lines = BufReader::new(reader).lines();
    loop {
        tokio::select! {
            line = lines.next_line() => match line {
                Ok(Some(line)) => {
                    let _ = app.emit(EVENT_LINE, line);
                }
                // The daemon closed the channel or it broke.
                _ => return true,
            },
            command = inbox.recv() => match command {
                Some(Command::Line(mut line)) => {
                    line.push('\n');
                    if writer.write_all(line.as_bytes()).await.is_err() {
                        return true;
                    }
                }
                Some(Command::Retry) => {}
                None => return false,
            },
        }
    }
}

/// The channel name. A development build talks to a daemon started with
/// `strelkad run`, whose channel is "strelka-dev", so that it never touches
/// an installed service; a release build talks to the installed one.
fn channel() -> String {
    match std::env::var(ENV_CHANNEL) {
        Ok(name) if !name.is_empty() => name,
        _ if cfg!(debug_assertions) => "strelka-dev".into(),
        _ => default_channel().into(),
    }
}

#[cfg(windows)]
fn default_channel() -> &'static str {
    "strelka"
}

#[cfg(windows)]
fn address() -> String {
    format!(r"\\.\pipe\{}", channel())
}

#[cfg(windows)]
async fn connect(address: &str) -> std::io::Result<tokio::net::windows::named_pipe::NamedPipeClient> {
    use tokio::net::windows::named_pipe::ClientOptions;

    // All instances of the pipe are taken for a moment while the daemon
    // makes the next one; that is not "no daemon".
    const ERROR_PIPE_BUSY: i32 = 231;
    const BUSY_TRIES: u32 = 20;

    let mut tries = 0;
    loop {
        match ClientOptions::new().open(address) {
            Ok(client) if installed_channel() => {
                server_is_a_service(&client)?;
                return Ok(client);
            }
            Ok(client) => return Ok(client),
            Err(err) if err.raw_os_error() == Some(ERROR_PIPE_BUSY) && tries < BUSY_TRIES => {
                tries += 1;
                time::sleep(Duration::from_millis(50)).await;
            }
            Err(err) => return Err(err),
        }
    }
}

/// Whether this is the installed service's channel: a release build with no
/// override. Only that one is checked for who is on the other end - a
/// developer's `strelkad run` is a process of their own.
#[cfg(windows)]
fn installed_channel() -> bool {
    !cfg!(debug_assertions) && std::env::var(ENV_CHANNEL).map_or(true, |name| name.is_empty())
}

/// Anyone logged on may create an instance of the pipe (the right to write
/// to it includes that), and while the real service is stopped - during an
/// update - a program could take the name and pose as it. Comparing the
/// server's path is out of reach: a user may not open a process of SYSTEM.
/// Its session is not: services live in session 0, and no user's program
/// can run there.
#[cfg(windows)]
fn server_is_a_service(client: &tokio::net::windows::named_pipe::NamedPipeClient) -> std::io::Result<()> {
    use std::os::windows::io::AsRawHandle;
    use windows::Win32::Foundation::HANDLE;
    use windows::Win32::System::Pipes::GetNamedPipeServerSessionId;

    let mut session = u32::MAX;
    unsafe { GetNamedPipeServerSessionId(HANDLE(client.as_raw_handle()), &mut session) }
        .map_err(|err| std::io::Error::other(format!("pipe server session: {err}")))?;
    if session != 0 {
        eprintln!("bridge: the channel is served from session {session}, not by the service; not connecting");
        return Err(std::io::Error::new(std::io::ErrorKind::PermissionDenied, "not the service"));
    }
    Ok(())
}

#[cfg(unix)]
fn default_channel() -> &'static str {
    "/run/strelka/strelkad.sock"
}

/// The same rule as core/internal/ipc: a name with a slash is a path, a
/// short name is a socket in the temporary directory.
#[cfg(unix)]
fn address() -> String {
    let name = channel();
    if name.contains('/') {
        return name;
    }
    std::env::temp_dir()
        .join(format!("{name}.sock"))
        .to_string_lossy()
        .into_owned()
}

#[cfg(unix)]
async fn connect(address: &str) -> std::io::Result<tokio::net::UnixStream> {
    tokio::net::UnixStream::connect(address).await
}
