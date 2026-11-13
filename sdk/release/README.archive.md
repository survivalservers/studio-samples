# Survival Servers Studio game server SDK @VERSION@ for @SYSTEM@

An optional library for a game server's side of the local API: say it is ready, get its match, report players,
labels and frame times, end itself, and hear when it must stop. You do not need it: every call is one plain HTTP
request your server can make itself. Apache-2.0.

## What is in it

| Folder | What |
|---|---|
| `include` | `gs_sdk.h` (C99, one stable header) and `gs_sdk.hpp` (C++17, header only, over the C library) |
| `lib` | The C library, static and shared |
| `csharp` | `GsSdk.dll`, the C# layer (.NET Standard 2.1) over the shared library |
| `go` | The Go module, as source: Go compiles the C library itself (cgo) |
| `rust` | The Rust crate, as source and as `.crate`: its build script compiles the C library itself |
| `samples` | The same small game server in C, C++, C#, Go and Rust, each set up to build against this folder |
| `tools` | `survivalservers`, the command-line tool: `survivalservers dev` runs a game server on your own computer the way our machines do. `THIRD_PARTY_NOTICES.txt` beside it holds the licences of Go and the modules built into it |

On Linux, `lib` has `libgs_sdk.a` and `libgs_sdk.so`, which needs glibc @GLIBC@ or newer. On
Windows it has `gs_sdk.dll` with its import library `gs_sdk.lib` and `gs_sdk.pdb`, and the static library
`gs_sdk_static.lib`, all built with Visual Studio 2022. The DLL carries its own C runtime and needs nothing
installed; the static library uses the DLL C runtime (`/MD`), as Unreal and most engines do.

When you link the shared library, define `GS_SDK_SHARED` in your build.

## Build and run a sample

From this folder. Each sample runs under the test agent as a ready server that gets match `m42` with `MAP=dunes`.

Linux:

```bash
cc -std=c99 -Iinclude samples/c/sample.c lib/libgs_sdk.a -lpthread -o sample-c
c++ -std=c++17 -Iinclude samples/cpp/sample.cpp lib/libgs_sdk.a -lpthread -o sample-cpp
dotnet build samples/csharp -c Release -o out/csharp
(cd samples/go && go build -o ../../sample-go .)
(cd samples/rust && cargo build --release) && cp samples/rust/target/release/gs-sample sample-rust
./tools/survivalservers dev -warm -allocate-after 1s -env MAP=dunes -external-id m42 -stop local_api -- ./sample-c
```

Windows, in a Developer Command Prompt for Visual Studio:

```bat
cl /nologo /MD /Iinclude samples\c\sample.c lib\gs_sdk_static.lib /Fe:sample-c.exe
cl /nologo /DGS_SDK_SHARED /Iinclude samples\c\sample.c lib\gs_sdk.lib /Fe:sample-c-dll.exe && copy lib\gs_sdk.dll .
cl /nologo /EHsc /std:c++17 /MD /Iinclude samples\cpp\sample.cpp lib\gs_sdk_static.lib /Fe:sample-cpp.exe
dotnet build samples\csharp -c Release -o out\csharp
tools\survivalservers.exe dev -warm -allocate-after 1s -env MAP=dunes -external-id m42 -stop local_api -- sample-c.exe
```

The Go module's path is `github.com/survivalservers/studio-go/sdk/go` until it is published; the Go sample points at the `go` folder with a
`replace` line. The Rust sample points at `rust/gs-sdk` by path.

The docs cover every call: https://studio.survivalservers.com/docs/game-server/sdk/
