//! A headless Bevy game server (no window, no renderer): Bevy's schedule runs the match at 64 updates a second, a
//! UDP socket carries the players, and the Rust SDK tells the platform it is ready, who is playing and how long each
//! update takes. It stops when the platform asks (the shutdown notice) or when SIGTERM ends it.
//!
//! Players send `join <id>`, `leave <id>` and `ping`; the server answers `welcome <id>`, `bye <id>`, `pong <tick>`.
//! Without the platform (no GS_LOCAL_API_URL) it runs as a plain local server.

use std::net::UdpSocket;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::time::{Duration, Instant};

use bevy::app::{AppExit, ScheduleRunnerPlugin};
use bevy::prelude::*;

const HZ: u32 = 64;

#[derive(Resource)]
struct Net(UdpSocket);

#[derive(Resource)]
struct Platform {
    gs: Option<Arc<gs_sdk::Client>>,
    stop: Arc<AtomicBool>,
}

#[derive(Resource, Default)]
struct Match {
    tick: u64,
    players: Vec<String>,
    frames: Vec<f64>,
    last_report: Option<Instant>,
}

fn main() {
    // Inside a Linux container the game listens on its fixed port (GS_PORT_GAME_INTERNAL) and the platform maps the
    // public one onto it; on Windows it listens on the public port itself (GS_PORT_GAME).
    let port: u16 = ["GS_PORT_GAME_INTERNAL", "GS_PORT_GAME"]
        .iter()
        .find_map(|k| std::env::var(k).ok().and_then(|p| p.parse().ok()))
        .unwrap_or(7777);
    let socket = UdpSocket::bind(("0.0.0.0", port)).expect("bind the game port");
    socket.set_nonblocking(true).expect("non-blocking socket");
    println!("listening on udp {port}");

    let stop = Arc::new(AtomicBool::new(false));
    let gs = match gs_sdk::Client::from_env() {
        Ok(c) => Some(Arc::new(c)),
        Err(e) => {
            println!("no platform ({e}); running as a plain local server");
            None
        }
    };
    if let Some(gs) = &gs {
        gs.ready().expect("ready");
        println!("ready");
        // The shutdown notice comes on the local API; the schedule checks the flag every update.
        let (gs, stop) = (gs.clone(), stop.clone());
        std::thread::spawn(move || loop {
            match gs.wait_shutdown(Duration::from_secs(60)) {
                Ok(Some(sd)) => {
                    println!("shutdown notice: {}", sd.reason);
                    stop.store(true, Ordering::SeqCst);
                    return;
                }
                Ok(None) => std::thread::sleep(Duration::from_millis(500)), // the wait ran out without a notice
                Err(e) => {
                    eprintln!("shutdown wait: {e}");
                    std::thread::sleep(Duration::from_secs(1));
                }
            }
        });
    }

    App::new()
        // Our own fixed-rate runner: Bevy's loop runner spins its core between updates (measured: a whole CPU for an
        // idle server), and on a shared machine an idle server should sleep.
        .add_plugins(MinimalPlugins.build().disable::<ScheduleRunnerPlugin>())
        .set_runner(|mut app: App| {
            let frame = Duration::from_secs_f64(1.0 / HZ as f64);
            loop {
                let t0 = Instant::now();
                app.update();
                if let Some(exit) = app.should_exit() {
                    return exit;
                }
                if let Some(rest) = frame.checked_sub(t0.elapsed()) {
                    std::thread::sleep(rest);
                }
            }
        })
        .insert_resource(Net(socket))
        .insert_resource(Platform { gs, stop })
        .init_resource::<Match>()
        .add_systems(Update, (players, report, stop_when_asked))
        .run();
    println!("match over");
}

fn players(net: Res<Net>, platform: Res<Platform>, mut m: ResMut<Match>) {
    let t0 = Instant::now();
    m.tick += 1;
    let mut buf = [0u8; 512];
    while let Ok((n, from)) = net.0.recv_from(&mut buf) {
        let msg = String::from_utf8_lossy(&buf[..n]).trim().to_string();
        let mut words = msg.splitn(2, ' ');
        let reply = match (words.next(), words.next()) {
            (Some("join"), Some(id)) => {
                if !m.players.iter().any(|p| p == id) {
                    m.players.push(id.to_string());
                    if let Some(gs) = &platform.gs {
                        let _ = gs.player_connect(id);
                    }
                }
                format!("welcome {id}")
            }
            (Some("leave"), Some(id)) => {
                m.players.retain(|p| p != id);
                if let Some(gs) = &platform.gs {
                    let _ = gs.player_disconnect(id);
                }
                format!("bye {id}")
            }
            (Some("ping"), _) => format!("pong {}", m.tick),
            _ => "?".to_string(),
        };
        let _ = net.0.send_to(reply.as_bytes(), from);
    }
    let ms = t0.elapsed().as_secs_f64() * 1000.0;
    m.frames.push(ms);
}

fn report(platform: Res<Platform>, mut m: ResMut<Match>) {
    let due = m.last_report.map_or(true, |t| t.elapsed() >= Duration::from_secs(1));
    if !due || m.frames.is_empty() {
        return;
    }
    let mut f = std::mem::take(&mut m.frames);
    f.sort_by(|a, b| a.partial_cmp(b).unwrap());
    let p50 = f[f.len() / 2];
    let p99 = f[(f.len() * 99 / 100).min(f.len() - 1)];
    if let Some(gs) = &platform.gs {
        let _ = gs.tick(p50, p99, HZ as i32);
    }
    m.last_report = Some(Instant::now());
}

fn stop_when_asked(platform: Res<Platform>, mut exit: MessageWriter<AppExit>) {
    if platform.stop.load(Ordering::SeqCst) {
        exit.write(AppExit::Success);
    }
}
