# Changes

What changed in each release of the game server SDK, newest first.

The SDK has two numbers. The release number (`release/VERSION`, and the Rust crate's `Cargo.toml`) names a download.
`GS_SDK_VERSION` in `gs_sdk.h` names the interface: a later one only adds functions.

## 0.1.0 (not yet released)

- The first release: the C library with its C++, C#, Go and Rust layers (interface version 3), and the
  `survivalservers` command-line tool, whose `dev` command runs a game server on your own computer.
- Interface version 3 adds the optional background helper (health on a timer, a callback for the match and one for
  the shutdown notice) and short retries for the calls that are safe to send twice. Players reports take turns, so
  the newest list lands last; a list past 1,000 IDs or 64 KiB goes as its count; a connection that does not open
  within 3 seconds fails the call.
