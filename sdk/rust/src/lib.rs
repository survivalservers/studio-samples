//! A thin Rust layer over the gs_sdk C library: the game server's side of the local API (ready, its match,
//! players, labels, health, update times, and the shutdown notice). Apache-2.0.

use std::ffi::{c_char, c_int, CStr, CString};
use std::fmt;
use std::time::Duration;

#[allow(non_camel_case_types)]
#[repr(C)]
struct gs_client {
    _private: [u8; 0],
}
#[allow(non_camel_case_types)]
#[repr(C)]
struct gs_allocation {
    _private: [u8; 0],
}

extern "C" {
    fn gs_open(out: *mut *mut gs_client) -> c_int;
    fn gs_open_with(url: *const c_char, token: *const c_char, out: *mut *mut gs_client) -> c_int;
    fn gs_close(c: *mut gs_client);
    fn gs_last_error(c: *const gs_client) -> *const c_char;
    fn gs_ready(c: *mut gs_client) -> c_int;
    fn gs_health(c: *mut gs_client) -> c_int;
    fn gs_stop(c: *mut gs_client, reason: *const c_char) -> c_int;
    fn gs_saved(c: *mut gs_client, keep: *const c_char) -> c_int;
    fn gs_wait_allocation(c: *mut gs_client, wait_ms: c_int, out: *mut *mut gs_allocation) -> c_int;
    fn gs_allocation_game_server_id(a: *const gs_allocation) -> *const c_char;
    fn gs_allocation_external_id(a: *const gs_allocation) -> *const c_char;
    fn gs_allocation_arg(a: *const gs_allocation, i: usize) -> *const c_char;
    fn gs_allocation_env_name(a: *const gs_allocation, i: usize) -> *const c_char;
    fn gs_allocation_env_value(a: *const gs_allocation, i: usize) -> *const c_char;
    fn gs_allocation_label_name(a: *const gs_allocation, i: usize) -> *const c_char;
    fn gs_allocation_label_value(a: *const gs_allocation, i: usize) -> *const c_char;
    fn gs_allocation_free(a: *mut gs_allocation);
    fn gs_wait_shutdown(c: *mut gs_client, wait_ms: c_int, deadline: *mut i64, reason: *mut c_char, size: usize) -> c_int;
    fn gs_player_connect(c: *mut gs_client, id: *const c_char) -> c_int;
    fn gs_player_disconnect(c: *mut gs_client, id: *const c_char) -> c_int;
    fn gs_set_player_count(c: *mut gs_client, count: c_int) -> c_int;
    fn gs_set_label(c: *mut gs_client, name: *const c_char, value: *const c_char) -> c_int;
    fn gs_tick(c: *mut gs_client, p50: f64, p99: f64, hz: c_int) -> c_int;
}

const OK: c_int = 0;
const NO_AGENT: c_int = -1;
const NONE_YET: c_int = -4;

/// A failed call: its code and the agent's or the library's reason.
#[derive(Debug, Clone)]
pub struct Error {
    pub code: i32,
    pub message: String,
}

impl fmt::Display for Error {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "gs {}: {}", self.code, self.message)
    }
}

impl std::error::Error for Error {}

/// The game server's match: its IDs, settings, labels and command-line arguments.
#[derive(Debug, Clone, Default)]
pub struct Allocation {
    pub game_server_id: String,
    pub external_id: String,
    pub args: Vec<String>,
    pub env: Vec<(String, String)>,
    pub labels: Vec<(String, String)>,
}

impl Allocation {
    /// One setting by name.
    pub fn env(&self, name: &str) -> Option<&str> {
        self.env.iter().find(|(k, _)| k == name).map(|(_, v)| v.as_str())
    }
}

/// A shutdown notice: save and exit before the deadline.
#[derive(Debug, Clone)]
pub struct Shutdown {
    pub deadline_unix_ms: i64,
    pub reason: String,
}

/// The game server's side of the local API. It may be shared between threads.
pub struct Client {
    c: *mut gs_client,
}

// The C client guards its own state; a waiting call blocks only its own thread.
unsafe impl Send for Client {}
unsafe impl Sync for Client {}

fn text(p: *const c_char) -> String {
    if p.is_null() {
        String::new()
    } else {
        unsafe { CStr::from_ptr(p) }.to_string_lossy().into_owned()
    }
}

fn cstr(s: &str) -> Result<CString, Error> {
    CString::new(s).map_err(|_| Error { code: -5, message: "a string with a NUL byte".into() })
}

fn millis(d: Duration) -> c_int {
    d.as_millis().min(c_int::MAX as u128) as c_int
}

impl Client {
    /// Connects as the game server the machine started (GS_LOCAL_API_URL, GS_TOKEN).
    pub fn from_env() -> Result<Client, Error> {
        let mut c = std::ptr::null_mut();
        match unsafe { gs_open(&mut c) } {
            OK => Ok(Client { c }),
            NO_AGENT => Err(Error { code: NO_AGENT, message: "no local API here (GS_LOCAL_API_URL and GS_TOKEN are not set)".into() }),
            r => Err(Error { code: r, message: "gs_open failed".into() }),
        }
    }

    pub fn open(agent_url: &str, token: &str) -> Result<Client, Error> {
        let (u, t) = (cstr(agent_url)?, cstr(token)?);
        let mut c = std::ptr::null_mut();
        match unsafe { gs_open_with(u.as_ptr(), t.as_ptr(), &mut c) } {
            OK => Ok(Client { c }),
            r => Err(Error { code: r, message: "gs_open_with failed".into() }),
        }
    }

    fn check(&self, r: c_int) -> Result<(), Error> {
        if r == OK {
            Ok(())
        } else {
            Err(Error { code: r, message: text(unsafe { gs_last_error(self.c) }) })
        }
    }

    pub fn ready(&self) -> Result<(), Error> {
        self.check(unsafe { gs_ready(self.c) })
    }

    pub fn health(&self) -> Result<(), Error> {
        self.check(unsafe { gs_health(self.c) })
    }

    /// The game server ends itself; a shutdown notice follows.
    pub fn stop(&self, reason: &str) -> Result<(), Error> {
        let r = cstr(reason)?;
        self.check(unsafe { gs_stop(self.c, r.as_ptr()) })
    }

    /// Waits up to `wait` for the match; `None` when there is none yet.
    /// A persistent server just finished a save: it is backed up at once, and with a name, kept under that name. An agent
    /// older than this call is not an error: the folder watcher backs saves up without it.
    pub fn saved(&self, keep: Option<&str>) -> Result<(), Error> {
        let k = keep.map(cstr).transpose()?;
        let r = unsafe { gs_saved(self.c, k.as_ref().map_or(std::ptr::null(), |k| k.as_ptr())) };
        if r == -8 {
            return Ok(()); // GS_ERR_UNSUPPORTED
        }
        self.check(r)
    }

    pub fn wait_allocation(&self, wait: Duration) -> Result<Option<Allocation>, Error> {
        let mut a = std::ptr::null_mut();
        let r = unsafe { gs_wait_allocation(self.c, millis(wait), &mut a) };
        if r == NONE_YET {
            return Ok(None);
        }
        self.check(r)?;
        let mut out = Allocation {
            game_server_id: text(unsafe { gs_allocation_game_server_id(a) }),
            external_id: text(unsafe { gs_allocation_external_id(a) }),
            ..Default::default()
        };
        for i in 0.. {
            let s = unsafe { gs_allocation_arg(a, i) };
            if s.is_null() {
                break;
            }
            out.args.push(text(s));
        }
        for i in 0.. {
            let n = unsafe { gs_allocation_env_name(a, i) };
            if n.is_null() {
                break;
            }
            out.env.push((text(n), text(unsafe { gs_allocation_env_value(a, i) })));
        }
        for i in 0.. {
            let n = unsafe { gs_allocation_label_name(a, i) };
            if n.is_null() {
                break;
            }
            out.labels.push((text(n), text(unsafe { gs_allocation_label_value(a, i) })));
        }
        unsafe { gs_allocation_free(a) };
        Ok(Some(out))
    }

    /// Waits up to `wait` for a shutdown notice; `None` when there is none.
    pub fn wait_shutdown(&self, wait: Duration) -> Result<Option<Shutdown>, Error> {
        let mut deadline = 0i64;
        let mut reason = [0 as c_char; 128];
        let r = unsafe { gs_wait_shutdown(self.c, millis(wait), &mut deadline, reason.as_mut_ptr(), reason.len()) };
        if r == NONE_YET {
            return Ok(None);
        }
        self.check(r)?;
        Ok(Some(Shutdown { deadline_unix_ms: deadline, reason: text(reason.as_ptr()) }))
    }

    /// Adds a player to the list the agent sees. An ID is 1 to 128 characters with no control characters; any other
    /// (often a name a player chose) is refused (code -5) and never joins the list.
    pub fn player_connect(&self, id: &str) -> Result<(), Error> {
        let i = cstr(id)?;
        self.check(unsafe { gs_player_connect(self.c, i.as_ptr()) })
    }

    pub fn player_disconnect(&self, id: &str) -> Result<(), Error> {
        let i = cstr(id)?;
        self.check(unsafe { gs_player_disconnect(self.c, i.as_ptr()) })
    }

    pub fn set_player_count(&self, count: i32) -> Result<(), Error> {
        self.check(unsafe { gs_set_player_count(self.c, count) })
    }

    pub fn set_label(&self, name: &str, value: &str) -> Result<(), Error> {
        let (n, v) = (cstr(name)?, cstr(value)?);
        self.check(unsafe { gs_set_label(self.c, n.as_ptr(), v.as_ptr()) })
    }

    pub fn remove_label(&self, name: &str) -> Result<(), Error> {
        let n = cstr(name)?;
        self.check(unsafe { gs_set_label(self.c, n.as_ptr(), std::ptr::null()) })
    }

    /// One second of update times (milliseconds) and the update rate, when known (0 otherwise).
    pub fn tick(&self, p50_ms: f64, p99_ms: f64, hz: i32) -> Result<(), Error> {
        self.check(unsafe { gs_tick(self.c, p50_ms, p99_ms, hz) })
    }
}

impl Drop for Client {
    fn drop(&mut self) {
        unsafe { gs_close(self.c) }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn refuses_a_bad_address() {
        assert!(Client::open("ftp://nowhere", "t").is_err());
        assert!(Client::open("http://127.0.0.1:1", "token").is_ok());
    }

    // The local API's address is read from GS_LOCAL_API_URL, or from its older name GS_AGENT_URL when the new one is not
    // set. The only test here that sets variables.
    #[test]
    fn opens_under_either_name() {
        std::env::set_var("GS_TOKEN", "token");
        std::env::set_var("GS_LOCAL_API_URL", "http://127.0.0.1:1");
        std::env::remove_var("GS_AGENT_URL");
        assert!(Client::from_env().is_ok(), "GS_LOCAL_API_URL");
        std::env::remove_var("GS_LOCAL_API_URL");
        std::env::set_var("GS_AGENT_URL", "http://127.0.0.1:1");
        assert!(Client::from_env().is_ok(), "GS_AGENT_URL");
        std::env::remove_var("GS_AGENT_URL");
        assert!(Client::from_env().is_err(), "neither");
    }
}
