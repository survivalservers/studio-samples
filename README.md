# Game server samples

Small game servers that run on the platform with no SDK, one per engine, and a script that takes any of them from
a build to a running game server. Apache-2.0: copy anything you need.

| Sample | Systems | State |
|---|---|---|
| `plain/` (any engine: the whole integration in about 300 lines of Go) | Windows zip, Linux image | Works and tested; hosted on Windows and Linux with the quick start script |
| `godot/` (Godot 4, GDScript, no plugin) | Linux image | Hosted with Godot 4.7.2 and 4.2.2 and joined from the internet; the same project also ran on Windows with Godot 4.7.2 |
| `godot-csharp/` (Godot 4 .NET, C#, with the C# SDK) | Windows zip, Linux image (Godot's dedicated server exports) | Hosted on Windows and Linux with Godot 4.7.2 .NET, joined from the internet |
| `bevy/` (Bevy 0.20, Rust, with the Rust SDK) | Windows zip, Linux image | Hosted on Windows and Linux, joined from the internet |
| `colyseus/` (Colyseus 0.18 on Node.js, TypeScript, no SDK) | Windows zip (with Node's `node.exe`), Linux image | Tested against a fake local API; hosted on Windows and Linux, joined from the internet |
| `defold/` (Defold 1.14, Lua, its headless engine, no extension) | Windows zip, Linux image | Tested against a fake local API; hosted on Windows and Linux, joined from the internet |
| `flax/` (Flax 1.13, C#, a game plugin run headless) | Windows zip, Linux image | Tested against a fake local API; hosted on Windows and Linux, joined from the internet |
| `o3de/` (O3DE 26.05, C++ in the project's gem, its HeadlessServerLauncher) | Linux image (Windows zip: same build on Windows) | Tested against a fake local API; hosted on Linux and Windows, joined from the internet |
| `godot-multiplayer-starter/` (Godot 4, GDScript: one project for the client and the server, with the add-on, a deploy script and notes for AI tools) | Windows zip, Linux image | Its tests run the server and a client headless with Godot (they skip where Godot is not installed) |
| `godot-addon/` (Godot 4: the Survival Servers Studio add-on, laid out for the Asset Library, with a demo) | Any Godot export (GDScript only) | Works and tested headless on Linux with Godot 4.7.2, installed into a fresh project too |
| `unity/` (a Unity package for Unity 2021.3 or newer, plain C#) | Windows zip, Linux image | Plain C# part tested in .NET; the Unity layer compiles against stand-ins for Unity's types; **never built in the Unity editor** |
| `unreal/` (Unreal Engine 5, a project with one game module, using our plugin in `unreal-plugin/`) | Windows zip, Linux image | Built with Unreal 5.8.4 from source; hosted on Windows and Linux, joined by packaged clients |
| `outpost/` (Go, on the Go SDK: short matches from ready servers, and a survival world run as a persistent server with saves and a console) | Windows zip, Linux image | Tested against a fake local API; runs on our test machines under simulated players |
| `quickstart/quickstart.sh` | Any build | Works and tested |

Unity servers themselves run here as they are: shipped Unity dedicated servers have been hosted and joined, Valheim
and Unturned on Windows and Linux, V Rising and Rust on Windows. The `unity/` package is the part not proven in the
engine.

## What every sample shows

A game server needs nothing from us to run: it is started with its ports in environment variables, its console is
its log, and exiting ends it. Each sample adds the optional parts, all plain HTTP on the local API:

- **Ports** from `GS_*` variables (`GS_PORT_GAME`, or `GS_PORT_GAME_INTERNAL` in a Linux image) and **match settings**
  from `GET /v0/self/allocation`, the only place secret values come from. Each sample reads it once before saying
  ready, so a fresh start's match is set up before its first player arrives; a ready server (no match yet) waits
  on `?wait=30s` after ready. The allocation file `GS_ALLOCATION_FILE` has the same data without secrets, for
  programs that make no HTTP calls.
- **Ready**: `POST $GS_LOCAL_API_URL/v0/self/ready` once the game port is bound. A version can instead watch for a log
  line (the samples print `Server ready for players`), a bound port, or a delay.
- **Ready servers**: a game server pre-started ahead of demand (a ready server) waits on that same call for its match.
- **Players**: `PUT /v0/self/players` with the connected player IDs.
- **Ending**: the match ends with `POST /v0/self/stop`; when we ask the game server to stop (Ctrl+Break on Windows,
  SIGTERM on Linux, or the `GET /v0/self/shutdown?wait=30s` long-poll), it saves and exits with code 0.
- **Results**: files written to `GS_ARTIFACT_DIR` are collected after the game server exits.

Every call sends `Authorization: Bearer $GS_TOKEN`. Without `GS_LOCAL_API_URL` the samples run as plain local servers.

## The plain sample

```bash
cd plain
go test ./...              # the whole life of a match against a fake local API
./build.sh                 # dist/plain-server-windows.zip and dist/linux/plain-server (+ an image with Docker)
GS_PORT_GAME=7777 ./dist/linux/plain-server
echo ping | nc -u -w1 127.0.0.1 7777        # pong; also "join <name>" and "leave <name>"
```

`version-windows.json` and `version-linux.json` are the version settings for each system. To see what a version's
memory size does, add `-memory-climb 8` to its args (8 MB more each second; `-memory-climb-to 470` stops there and
holds it): above 90% of the size for a minute brings `game_server.memory_high`, and past the size the game server ends
`out_of_memory`.

## The Godot sample

`server.gd` is the whole server: Godot's own `PacketPeerUDP` for the game and `HTTPRequest` for the local API.

```bash
cd godot
godot --headless --path .                        # a local server on port 7777
GODOT=/path/to/godot go test ./                  # the life of a match against a fake local API
docker build -t godot-server .                   # the Linux image
```

Godot does not catch SIGTERM, so its version stops it through the local API (`"stop": {"method":
"local_api"}` in `version-linux.json`): the server waits for that notice, saves and exits.

## The Godot C# sample

`Server.cs` is the whole server, with .NET's `UdpClient` for the game and the C# SDK (`../sdk/csharp`) for the
local API. Godot needs a solution file to export C#, and the SDK needs its C library beside the game:

```bash
cd godot-csharp
dotnet build                                      # the C# part (Godot.NET.Sdk from NuGet)
godot --headless --path . --export-release Linux out/linux/server.x86_64     # the presets are dedicated servers
godot --headless --path . --export-release Windows out/windows/server.exe
cc -O2 -shared -fPIC -fvisibility=hidden -DGS_SDK_SHARED -D_POSIX_C_SOURCE=200809L -I../sdk/c/include \
  ../sdk/c/src/gs_sdk.c -lpthread -o out/linux/data_GodotCsServer_linuxbsd_x86_64/libgs_sdk.so
```

On Windows put `gs_sdk.dll` (the SDK README's lines) in `data_GodotCsServer_windows_x86_64`. The exports carry their
own .NET runtime, so the machine needs none; the project asks for invariant globalization, so no ICU library either.
The server caps itself at 30 frames a second (`Engine.MaxFps`), which keeps it near idle on Windows.

## The Colyseus sample

`src/server.ts` is the whole server: one Colyseus room, `match`, on the WebSocket transport, and Node's own `fetch`
for the local API. Players join with the option `name`; `hello` answers `welcome`, and `ping` answers `pong` with the
server's tick. The match runs at 20 updates a second and reports its tick times. `src/client.ts` is a scripted player
on Colyseus' client SDK: the tests use it, and so can you against a running server.

```bash
cd colyseus
./build.sh                    # dist/server.cjs, dist/client.cjs and dist/colyseus-server-windows.zip (+ an image with Docker)
GS_PORT_GAME=2567 node dist/server.cjs
node dist/client.cjs ws://127.0.0.1:2567 --players ana,bo --hold 5
go test ./                    # the life of a match against a fake local API (from oss/samples, after the build)
```

Each build is one file with its packages inside (esbuild), so it needs only Node. The Windows zip carries Node's own
`node.exe` (checked against nodejs.org's checksum); its version runs `node.exe server.cjs`. The Linux image is the
`Dockerfile`: Node's slim image, the bundle, and its non-root user. The port is TCP (`"protocol": "tcp"`), since
Colyseus runs on WebSocket. The server saves its result and exits with code 0 on the shutdown notice, SIGTERM (Linux) and
Ctrl+Break (Windows, which Node sees as SIGBREAK), so its versions use the systems' usual stops, `sigterm` and
`ctrl_break`. Colyseus' own signal handlers are off (`gracefullyShutdown: false`): they do not know SIGBREAK.

## The Defold sample

`main/server.script` is the whole server, on Defold's headless engine (`dmengine_headless`, which Defold's builder
bundles with `--variant headless`): LuaSocket, built into Defold, carries both the game's UDP and the local API calls.
Defold's own `http` module is not there in the headless engine (1.14), so the script speaks plain HTTP/1.0 over a TCP
socket, one coroutine per call, moved on every frame so a long-poll never holds the game up. The game is the same
UDP lines as the Godot sample (`join <name>`, `leave <name>`, `ping`), at 30 frames a second
(`display.update_frequency`).

```bash
cd defold
./build.sh                    # Java 25: dist/linux/DefoldServer/ and dist/defold-server-windows.zip
GS_PORT_GAME=7777 dist/linux/DefoldServer/DefoldServer.x86_64
echo ping | nc -u -w1 127.0.0.1 7777        # pong
go test ./                    # the life of a match against a fake local API (from oss/samples, after the build)
```

The headless engine is a debug build: it opens Defold's engine service on TCP 8001 and a log server. The versions set
`DM_SERVICE_PORT=dynamic`, so two servers on one Windows machine never fight over 8001; a Linux image publishes only
its game port anyway. It never opens a window or a sound device, but it still links the X11 and OpenAL libraries, so
the `Dockerfile` installs them. Defold has no stop signal handler of its own, so both versions stop through the local
API (`"stop": {"method": "local_api"}`): the server takes the shutdown notice, saves its result and exits with code 0.

## The Flax sample

`Source/Game/LabServer.cs` is the whole server: a Flax game plugin, so it starts with the game and needs no scene of
its own (the project's first scene is empty), with .NET's `UdpClient` for the game and `HttpClient` for the local
API. It is the same UDP lines as the Godot sample, at 30 updates a second. The editor builds it with no window or
graphics card, and the game carries its own .NET runtime:

```bash
cd flax
FLAX_EDITOR=/path/to/FlaxEditor ./build.sh Linux     # Flax 1.13 with its Linux platform tools, and the .NET 10 SDK
Output/Linux/FlaxServer -headless -null -mute -std   # a local server on port 7777
go test ./                    # the life of a match against a fake local API (from oss/samples, after the build)
```

On Windows, `FlaxEditor.exe ... -build Server.Windows` (or `build.sh Windows` there) builds `Output/Windows` and
`dist/flax-server-windows.zip`, whose executable is `FlaxServer.exe` with the same four arguments. The plugin does
nothing inside the editor, which loads game plugins too. Flax catches no stop signal for the game, so both versions
stop through the local API (`local_api`). Flax's engine still links the X11, Wayland and GLib libraries and .NET needs
ICU, so the `Dockerfile` installs them. The editor reads `-project=PATH` with an equals sign: a path after a space
that starts with `/` reads as another option.

## The O3DE sample

`o3de/` holds only what we add to O3DE's own MinimalProject template: `Gem/Source/LabGame.cpp` is the whole server
(plain sockets for the game's UDP lines and for the local API over HTTP/1.0 on a thread of its own), and the
project's system component runs it on the game's tick, only inside a server launcher. `build.sh` makes the project
from the template, copies our files in and builds `LabServer.HeadlessServerLauncher` (profile) against the SDK, in
seconds, since the engine comes prebuilt:

```bash
cd o3de
O3DE=/opt/O3DE/26.05 ./build.sh        # CMake 3.30+, clang and O3DE's Linux packages: dist/linux/{bin,project}
dist/linux/bin/LabServer.HeadlessServerLauncher --project-path=../project -rhi=null -bg_ConnectToAssetProcessor=0
go test ./                             # the life of a match against a fake local API (from oss/samples, after the build)
```

A server that draws nothing needs no processed assets once it loads no renderer: the SDK's server launchers would load
Atom, ImGui, EMotionFX and the camera, and Atom stops the launcher when its compiled shaders are missing, so the build
leaves those gems out of the headless launcher's module list, and leaves out the template's `defaultlevel` (it does
not exist). The launcher also opens O3DE's remote console on TCP 4600; a Linux image publishes only the game's port.
Both versions stop through the local API (`local_api`).

## The Godot add-on

`godot-addon/` is laid out the way the Godot Asset Library expects: the add-on in `addons/game_server` (with its
licence), a demo project around it, and a `.gitattributes` that keeps the demo and tests out of the download. Turned on
under Project Settings > Plugins it adds the `GameServer` autoload: `port()`, `ready_for_players()`, the `allocated`
and `shutdown_requested` signals, `player_connect()` and `player_disconnect()`, `set_player_count()`, `set_label()`,
`remove_label()`, `tick()`, `health()` and `stop()`. Its README has the details.

```bash
cd godot-addon
godot --headless --path .                        # the demo, a local server on port 7777
GODOT=/path/to/godot go test ./                  # the demo against a fake local API, and a fresh install
```

## The Unity sample

A Unity package (`unity/`, add it by path or git URL). Its plain C# part (`Runtime/Core`: the game port, the match
rules and every local API call) has no UnityEngine types, so it also runs in plain .NET; the Unity layer is one
component, `GameServerBehaviour`, that calls it every frame through `UnityWebRequest`, plus an editor script for
dedicated server builds (menu Game Server, or `-executeMethod GameServerSample.Editor.GameServerBuild.Windows`).
`LocalApi` uses the custom-engine SDK's names: `Ready`, `Health`, `Stop`, `WaitAllocation`, `WaitShutdown`,
`PlayerConnect` and `PlayerDisconnect` (it keeps the list and sends it whole), `SetPlayerCount`, `SetLabel`,
`RemoveLabel` and `Tick`; waits are at most 60 seconds.

```bash
cd unity
dotnet test "Tests~/Core"               # the plain C# part: match life, game protocol, settings, HTTP calls
dotnet build "Tests~/EngineCompile"     # the Unity layer against stand-ins for the engine types (a compile check)
```

Both versions stop through the local API. The package has never run in a Unity player, so how it takes Ctrl+Break
and SIGTERM is not checked.

## The Unreal sample

A project (`unreal/`) with one game module, using the Survival Servers Studio plugin (`unreal-plugin/`, copied
into `unreal/Plugins` by `build-server.bat`). In Unreal the game port is the engine's own (`-Port={{port.game}}` in
the version's launch arguments), so a server needs no code at all to run. The plugin's `USurvivalServersSubsystem`
adds the optional calls: ready once the map is loaded, the match data, players from the game mode's logins and
logouts, frame times, the shutdown notice, a self-stop when the last player leaves (the sample turns that on in
`Config/DefaultGame.ini`), and a result file. Its calls carry the custom-engine SDK's names (`Ready`, `Health`,
`Stop`, `WaitAllocation`, `WaitShutdown`, `PlayerConnect`, `PlayerDisconnect`, `SetPlayerCount`, `SetLabel`,
`RemoveLabel`, `Tick`) on Unreal's own non-blocking HTTP. The sample's game mode shows what a game adds: a label and
a save when a shutdown notice arrives. The plugin's rules and request bodies are plain C++ with tests that build without
the engine:

```bash
unreal-plugin/tests/run.sh              # g++, without exceptions or RTTI, as Unreal builds modules
```

`build-server.bat` packages the Windows zip and the Linux build with RunUAT; dedicated server targets need Unreal
built from source. The ZIP keeps its `WindowsServer/` folder so its executable, log and crash paths match
`version-windows.json`. Check that packaging contract without the engine using
`pwsh -NoProfile -File unreal/tests/archive_layout_test.ps1`.

## Not built in an engine

The Unity package has never been opened in the Unity editor: its build script, its `UnityWebRequest` calls and its
stop handling are untested in a Unity player. Before you rely on it, build both server targets in your own project and
play one match on the local test agent.

## Quick start script

```bash
export API=https://studio.survivalservers.com API_KEY=gsk_live_...
quickstart/quickstart.sh windows plain/dist/plain-server-windows.zip plain/version-windows.json
quickstart/quickstart.sh linux   registry.example.com/you/plain-server:1.0.0 plain/version-linux.json
```

It makes the app, uploads the build (a zip in parts, straight to storage), makes the version, waits until a region
has it, starts a game server and prints the address players connect to. `CONTINENT` picks where (default
`europe`, where a region is open today). `TIER` picks the tier (default `high-frequency` on Linux and `standard` on
Windows, what that region sells). `PRACTICE=1` runs it against the practice server, which answers every call with an example.
`go test ./quickstart` runs it against a fake of the API.
