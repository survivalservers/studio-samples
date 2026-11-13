# Changes

What changed in each release of the game server SDK, newest first.

The SDK has two numbers. The release number (`release/VERSION`, and the Rust crate's `Cargo.toml`) names a download.
`GS_SDK_VERSION` in `gs_sdk.h` names the interface: a later one only adds functions.

## 0.1.0 (not yet released)

- The first release: the C library with its C++, C#, Go and Rust layers (interface version 2), and the
  `survivalservers` command-line tool, whose `dev` command runs a game server on your own computer.
