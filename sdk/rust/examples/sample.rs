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
