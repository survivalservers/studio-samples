# gs_sdk

A small optional library for a game server's side of the local API: say it is ready, report health, get its
match, report players, labels and update times, end itself, and hear when it must stop. A C library with one stable
header, plus thin layers for C++, C#, Rust and Go. Apache-2.0, standard C and the system's sockets only: copy the
folder into your tree and build it with the rest of your server.

You do not need it. Every call is one plain HTTP request a game server can make itself (see `../samples`, which use
no SDK, and `../localapi/openapi.yaml`). It is for engines with no HTTP client at hand, and for skipping the JSON.

## The calls

| What | C | C++ / C# / Go | Rust |
|---|---|---|---|
| Open from `GS_LOCAL_API_URL` and `GS_TOKEN` | `gs_open` | `FromEnvironment` (Go: `FromEnv`) | `from_env` |
| Ready for players (a ready server: waiting for its match) | `gs_ready` | `Ready` | `ready` |
| Still alive | `gs_health` | `Health` | `health` |
| Wait for the match (IDs, settings, labels, arguments) | `gs_wait_allocation` | `WaitAllocation` | `wait_allocation` |
| A player joined / left | `gs_player_connect` / `gs_player_disconnect` | `PlayerConnect` / `PlayerDisconnect` | `player_connect` / `player_disconnect` |
| Player count, for games without player IDs | `gs_set_player_count` | `SetPlayerCount` | `set_player_count` |
| Set or remove a label | `gs_set_label` (value `NULL` removes) | `SetLabel` / `RemoveLabel` | `set_label` / `remove_label` |
| Update times for the last second | `gs_tick` | `Tick` | `tick` |
| End itself (the match is over) | `gs_stop` | `Stop` | `stop` |
| A persistent server just finished a save | `gs_saved` | `Saved` | `saved` |
| Wait for a shutdown notice: save and exit before its deadline | `gs_wait_shutdown` | `WaitShutdown` | `wait_shutdown` |

Each sample (`c/sample`, `cpp/sample.cpp`, `csharp/Sample`, `rust/examples/sample.rs`, `go/sample`) makes the same
calls in the same order, in about 30 lines.

What to know:

- **Not under an agent?** `gs_open` returns `GS_ERR_NO_AGENT` (the other layers return that error), so the same
  build can run as a plain local server.
- **Players are the pause signal.** The client keeps the list and sends it whole on every connect and disconnect.
  A persistent server set to pause when empty (`when_empty`) is paused once its count stays at zero for the time
  set, and the next player's first packet resumes it.
  Report players promptly, and never report zero while someone is still playing.
- **Player IDs** are 1 to 128 characters with no control characters; a connect with any other ID (often a name a
  player chose) is refused (`GS_ERR_ARGUMENT`) and that player never joins the list, so the others keep counting.
- **Waiting calls** hold one connection open for up to 60 seconds and block only the thread that makes them; on
  `GS_ERR_NONE_YET`, call again. `gs_wait_allocation` also returns early when a stop is requested meanwhile.
- **Stopping:** a version's stop method may also send a signal (SIGTERM on Linux, Ctrl+Break on Windows). A game
  that waits for the shutdown notice gets the notice first, and the signal only if it is still running halfway through
  its shutdown time; a game that never waits for the notice gets both at once. The samples run with the stop method
  `local_api` (the notice only); a real game should handle the signal too, or use that stop method.
- **Threads:** one client may be used from several threads. Each call uses its own connection.
- **Secrets:** settings marked secret arrive only through the allocation call. The library never writes them out.
- **Errors** are negative `gs_result` codes; `gs_last_error` (or the exception or error value) says why. A call the
  agent turned down, including "too many calls", is `GS_ERR_REFUSED` with the agent's reason.
- **Compatibility:** `GS_SDK_VERSION` is 2. Later versions only add functions; nothing here changes meaning.

## Building it into a game

| Layer | What to add | Needs |
|---|---|---|
| C | `c/src/gs_sdk.c` and `c/include` (C99) | Linux: `-lpthread`. Windows: `ws2_32` (MSVC links it by itself) |
| C (CMake) | `add_subdirectory(c)`: `gs_sdk` (static), `gs_sdk_shared` (`gs_sdk.dll` / `libgs_sdk.so`), `gs_sample` | CMake 3.16 |
| C++ | `cpp/gs_sdk.hpp` (header-only) over the C library | C++17 |
| C# | `csharp/GsSdk` (netstandard2.1; tested on .NET 8, and in Godot .NET in our `godot-csharp` sample) | The shared library beside the program: `gs_sdk.dll` or `libgs_sdk.so` |
| Rust | the `rust` crate; `build.rs` compiles the C file | The `cc` build crate. It reads `../c`, so keep the folder whole |
| Go | `github.com/survivalservers/studio-go/sdk/go` (cgo; compiles the C file itself) | A C compiler: gcc or clang; MinGW on Windows |

The shared library: define `GS_SDK_SHARED` both when building it and when building against it (it marks the
exports on Windows and hides everything else elsewhere).

Windows by hand (the MinGW line is what the tests run; the MSVC lines, Build Tools 2022, built clean at `/W4` and their samples passed the C test's checks under the local test, now `survivalservers dev`):

```
cl /c /O2 /I c\include c\src\gs_sdk.c && lib gs_sdk.obj                          :: static, MSVC
cl /LD /O2 /DGS_SDK_SHARED /I c\include c\src\gs_sdk.c /Fe:gs_sdk.dll ws2_32.lib  :: gs_sdk.dll
x86_64-w64-mingw32-gcc -O2 -shared -DGS_SDK_SHARED -Ic/include c/src/gs_sdk.c -lws2_32 -o gs_sdk.dll
```

Linux by hand:

```
cc -O2 -shared -fPIC -fvisibility=hidden -DGS_SDK_SHARED -D_POSIX_C_SOURCE=200809L -Ic/include c/src/gs_sdk.c -lpthread -o libgs_sdk.so
```

Unreal: our plugin (`unreal-plugin/` in the samples) makes the same calls on Unreal's own HTTP, and is what has been
built and hosted (Unreal 5.8). Compiling `gs_sdk.c` into a game module with the C++ header has not been tried in
Unreal. Unity: the Unity package (`unity/` in the samples) makes the same calls over Unity's own HTTP. Neither it nor
this C# layer has ever been built in Unity, so where a Unity project would take the native library is untested.

## Release archives

The prebuilt archives, `survival-servers-studio-sdk-<version>-linux-x64.tar.gz` and `...-windows-x64.zip` with
`SHA256SUMS`, hold the C library (Linux: `libgs_sdk.a`, `libgs_sdk.so`; Windows: `gs_sdk.dll` with `gs_sdk.lib` and
`gs_sdk.pdb`, and `gs_sdk_static.lib`), both headers, `GsSdk.dll`, the Go module and the Rust crate as source, the
samples set up to build against the archive, and the command-line tool `survivalservers`. `release/build-windows.ps1` builds the Windows libraries
with Visual Studio 2022; `release/build.sh` builds the rest and both archives; the version is `release/VERSION`.

`release/test-linux.sh` (in a fresh Ubuntu 24.04 container) and `release/test-windows.ps1` (in a new folder on
Windows) build every sample against an archive and run it under the archive's `survivalservers dev`, with the same checks as
the tests below.

## Tests

```
go test ./...
```

builds the command-line tool (`../cli`, whose `dev` command is `../localapi/dev`) and runs every language's sample under it as a ready server
(pre-started) that gets match `m42`. Each must make every call above and exit cleanly after its shutdown notice. A
language whose toolchain is missing is skipped. `TestWindowsBuilds` cross-compiles the DLL and the C, C++, Go and Rust
samples with MinGW; they run on a Windows machine. `go/gs_test.go` checks the HTTP and JSON handling against a fake
agent (chunked replies, escapes, refusals, the deadline).

To try a sample by hand:

```
go build -C ../cli -o "$PWD/survivalservers" ./cmd/survivalservers
./survivalservers dev -warm -allocate-after 1s -env MAP=dunes -external-id m42 -stop local_api -- ./sample
```
