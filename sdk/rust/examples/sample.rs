// The smallest game server with the Rust layer; see ../../c/sample/sample.c for what it shows.
use std::time::Duration;

fn main() {
    if let Err(e) = run() {
        eprintln!("{e}");
        std::process::exit(1);
    }
}

fn run() -> Result<(), gs_sdk::Error> {
    let gs = gs_sdk::Client::from_env()?;
    if std::env::var_os("GS_SAMPLE_BACKGROUND").is_some() {
        return background(std::sync::Arc::new(gs));
    }
    gs.ready()?;
    let m = gs.wait_allocation(Duration::from_secs(30))?.expect("no match within 30 s");
    println!("match {} map={}", m.external_id, m.env("MAP").unwrap_or("-"));
    gs.player_connect("p1")?;
    gs.player_connect("p2")?;
    // A name a player chose, with a control character in it: refused, and p1 and p2 still count.
    assert!(gs.player_connect("bad\u{1}name").is_err(), "a player ID with a control character was taken");
    println!("refused a player ID with a control character");
    gs.player_disconnect("p2")?;
    gs.set_label("mode", "ffa")?;
    gs.health()?;
    gs.tick(2.5, 6.25, 64)?;
    gs.stop("match_over")?;
    match gs.wait_shutdown(Duration::from_secs(30))? {
        Some(sd) => println!("shutdown reason={} deadline={}", sd.reason, if sd.deadline_unix_ms > 0 { "set" } else { "none" }),
        None => println!("shutdown reason=none deadline=none"),
    }
    Ok(())
}

// With GS_SAMPLE_BACKGROUND set, the library's background helper sends health and calls the two closures once each.
fn background(gs: std::sync::Arc<gs_sdk::Client>) -> Result<(), gs_sdk::Error> {
    use std::sync::mpsc;
    let (matched, match_seen) = mpsc::channel();
    let (stopped, stop_seen) = mpsc::channel();
    let helper = gs.start_background(
        Duration::from_millis(200),
        Some(Box::new(move |m: gs_sdk::Allocation| {
            println!("match {} map={}", m.external_id, m.env("MAP").unwrap_or("-"));
            let _ = matched.send(());
        })),
        Some(Box::new(move |sd: gs_sdk::Shutdown| {
            let _ = stopped.send(sd);
        })),
    )?;
    gs.ready()?;
    let _ = match_seen.recv_timeout(Duration::from_secs(30));
    gs.player_connect("p1")?;
    gs.set_label("mode", "ffa")?;
    std::thread::sleep(Duration::from_millis(1500)); // the helper sends health meanwhile
    gs.stop("match_over")?;
    match stop_seen.recv_timeout(Duration::from_secs(30)) {
        Ok(sd) => println!("shutdown reason={} deadline={}", sd.reason, if sd.deadline_unix_ms > 0 { "set" } else { "none" }),
        Err(_) => println!("shutdown reason=none deadline=none"),
    }
    helper.stop();
    Ok(())
}
