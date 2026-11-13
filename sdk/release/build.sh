#!/usr/bin/env bash
# Builds the SDK's release archives: one for Linux x64 (tar.gz) and, given the Windows libraries, one for Windows x64
# (zip), with SHA256SUMS beside them. Nothing is published.
#
#   release/build.sh OUT_DIR [WINDOWS_LIB_DIR]
#
# WINDOWS_LIB_DIR holds what build-windows.ps1 made with Microsoft's compiler (gs_sdk.dll, gs_sdk.lib, gs_sdk.pdb,
# gs_sdk_static.lib); without it only the Linux archive is made.
#
# Each archive has the C library (static and shared) and its header, the C++ header, the C# layer (GsSdk.dll,
# netstandard2.1), the Go module and the Rust crate as source (that is how Go and Rust take a C library: each compiles
# gs_sdk.c itself), the samples set up to build against the archive, and the command-line tool (survivalservers), whose dev command
# runs a game server on a developer's PC as our machines would.
# Needs: gcc, the .NET 8 SDK, Go 1.22+, cargo (with the cc crate fetched or a network), python3, tar.
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
SDK=$(cd "$HERE/.." && pwd)
OSS=$(cd "$SDK/.." && pwd)
OUT=${1:?usage: build.sh OUT_DIR [WINDOWS_LIB_DIR]}
WIN=${2:-}
VERSION=$(cat "$HERE/VERSION")
NAME=survival-servers-studio-sdk-$VERSION
COMMIT=$(git -C "$SDK" rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
GLIBC_FLOOR=2.34

die() { echo "build.sh: $*" >&2; exit 1; }
# No stand-in link ships (docs.example.com and the like); a sample's own module path (example.com/gs-sample) is not a link.
no_stand_ins() {
	if grep -rnIE '://([a-z0-9-]+\.)*example\.com' "$1" >&2; then die "^ a stand-in address in $(basename "$1"): name the real one"; fi
}
for tool in gcc dotnet go cargo python3 tar objdump; do command -v "$tool" >/dev/null || die "$tool is not installed"; done
[ -z "$WIN" ] || for f in gs_sdk.dll gs_sdk.lib gs_sdk_static.lib; do [ -s "$WIN/$f" ] || die "no $f in $WIN"; done
[ ! -e "$OUT" ] || die "$OUT already exists: give a new folder"
mkdir -p "$OUT"
OUT=$(cd "$OUT" && pwd)
WORK=$OUT/work
mkdir -p "$WORK"
export DOTNET_CLI_TELEMETRY_OPTOUT=1 DOTNET_NOLOGO=1 MSBUILDDISABLENODEREUSE=1 DOTNET_CLI_USE_MSBUILD_SERVER=0

# The parts both archives share, in $WORK/common.
common=$WORK/common
mkdir -p "$common"/{include,csharp,go,rust,samples,tools}
cp "$SDK/LICENSE" "$SDK/NOTICE" "$common/"
printf '%s (commit %s, SDK interface version %s)\n' "$VERSION" "$COMMIT" \
	"$(sed -n 's/^#define GS_SDK_VERSION \([0-9]*\).*/\1/p' "$SDK/c/include/gs_sdk.h")" > "$common/VERSION"
cp "$SDK/c/include/gs_sdk.h" "$SDK/cpp/gs_sdk.hpp" "$common/include/"

# C#: the netstandard2.1 layer, built from a copy so obj/ and bin/ stay out of the tree.
cp -r "$SDK/csharp/GsSdk" "$WORK/GsSdk"
rm -rf "$WORK/GsSdk/bin" "$WORK/GsSdk/obj"
dotnet build "$WORK/GsSdk" -c Release -o "$WORK/GsSdk-out" --nologo -v q -p:Version="$VERSION" \
	--disable-build-servers -p:UseSharedCompilation=false -nodeReuse:false >/dev/null
cp "$WORK/GsSdk-out/GsSdk.dll" "$common/csharp/"

# Go: the module as source (go.mod, the C library, the Go layer), without the repository's tests.
gomod=$common/go
cp "$SDK/go.mod" "$SDK/LICENSE" "$SDK/NOTICE" "$gomod/"
mkdir -p "$gomod/c" "$gomod/go"
cp -r "$SDK/c/include" "$SDK/c/src" "$gomod/c/"
cp "$SDK/go/gs.go" "$SDK/go/gs_sdk.c" "$gomod/go/"

# Rust: the crate as cargo packages it, with the C library inside it, checked by building the packaged copy.
crate=$WORK/crate
mkdir -p "$crate"
cp -r "$SDK/rust/src" "$SDK/rust/examples" "$SDK/rust/build.rs" "$SDK/LICENSE" "$SDK/NOTICE" "$crate/"
sed "s/^version = \".*\"/version = \"$VERSION\"/" "$SDK/rust/Cargo.toml" > "$crate/Cargo.toml"
mkdir -p "$crate/c"
cp -r "$SDK/c/include" "$SDK/c/src" "$crate/c/"
(cd "$crate" && cargo package --quiet --offline --locked --allow-dirty --target-dir "$WORK/cargo-target" 2>&1) ||
	(cd "$crate" && cargo package --quiet --allow-dirty --target-dir "$WORK/cargo-target")
cratefile=$WORK/cargo-target/package/gs-sdk-$VERSION.crate
[ -s "$cratefile" ] || die "cargo made no $cratefile"
cp "$cratefile" "$common/rust/"
tar -xzf "$cratefile" -C "$common/rust" && mv "$common/rust/gs-sdk-$VERSION" "$common/rust/gs-sdk"

# The samples, each set up to build against this archive.
s=$common/samples
mkdir -p "$s"/{c,cpp,csharp,go,rust/src}
cp "$SDK/c/sample/sample.c" "$s/c/"
cp "$SDK/cpp/sample.cpp" "$s/cpp/"
cp "$SDK/csharp/Sample/Program.cs" "$s/csharp/"
cat > "$s/csharp/Sample.csproj" <<'EOF'
<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <OutputType>Exe</OutputType>
    <TargetFramework>net8.0</TargetFramework>
    <Nullable>enable</Nullable>
  </PropertyGroup>
  <ItemGroup>
    <Reference Include="GsSdk" HintPath="../../csharp/GsSdk.dll" />
    <!-- The native library goes beside the program: libgs_sdk.so on Linux, gs_sdk.dll on Windows. -->
    <None Include="../../lib/libgs_sdk.so" Condition="Exists('../../lib/libgs_sdk.so')" CopyToOutputDirectory="PreserveNewest" Visible="false" />
    <None Include="../../lib/gs_sdk.dll" Condition="Exists('../../lib/gs_sdk.dll')" CopyToOutputDirectory="PreserveNewest" Visible="false" />
  </ItemGroup>
</Project>
EOF
cp "$SDK/go/sample/main.go" "$s/go/main.go"
cat > "$s/go/go.mod" <<'EOF'
module example.com/gs-sample

go 1.22.0

require github.com/survivalservers/studio-go/sdk v0.0.0

replace github.com/survivalservers/studio-go/sdk => ../../go
EOF
sed 's/^\/\/ The smallest game server with the Rust layer; see ..\/..\/c\/sample\/sample.c/\/\/ The smallest game server with the Rust layer; see ..\/..\/c\/sample.c/' \
	"$SDK/rust/examples/sample.rs" > "$s/rust/src/main.rs"
cat > "$s/rust/Cargo.toml" <<EOF
[package]
name = "gs-sample"
version = "0.1.0"
edition = "2021"
publish = false

[dependencies]
gs-sdk = { path = "../../rust/gs-sdk" }
EOF

# The command-line tool, for both systems (pure Go, no C compiler): its dev command is the local test.
for target in linux/amd64 windows/amd64; do
	ext=; [ "${target%/*}" = windows ] && ext=.exe
	(cd "$OSS/cli" && CGO_ENABLED=0 GOOS=${target%/*} GOARCH=${target#*/} go build -trimpath -ldflags=-s \
		-o "$WORK/survivalservers-${target%/*}$ext" ./cmd/survivalservers)
	# Its third-party notices (Go and each module built into it), read from that build, go beside it.
	"$OSS/cli/notices.sh" "$WORK/survivalservers-${target%/*}$ext" "$WORK/notices-${target%/*}.txt" >/dev/null
done

# Linux x64: the C library built here, static and shared.
linux=$WORK/$NAME-linux-x64
cp -r "$common" "$linux"
mkdir -p "$linux/lib"
gcc -std=c99 -O2 -fPIC -Wall -Wextra -Werror -D_POSIX_C_SOURCE=200809L -I"$SDK/c/include" -c "$SDK/c/src/gs_sdk.c" -o "$WORK/gs_sdk.o"
ar rcs "$linux/lib/libgs_sdk.a" "$WORK/gs_sdk.o"
gcc -std=c99 -O2 -shared -fPIC -fvisibility=hidden -DGS_SDK_SHARED -D_POSIX_C_SOURCE=200809L -I"$SDK/c/include" \
	"$SDK/c/src/gs_sdk.c" -lpthread -Wl,-soname,libgs_sdk.so -o "$linux/lib/libgs_sdk.so"
# The newest C library version the shared library needs (its README says it): no newer than 2.34, so it loads on
# Ubuntu 22.04, Debian 12 and RHEL 9 and later.
need=$(objdump -T "$linux/lib/libgs_sdk.so" | sed -n 's/.*GLIBC_\([0-9.]*\).*/\1/p' | sort -V | tail -1)
[ "$(printf '%s\n%s\n' "$need" "$GLIBC_FLOOR" | sort -V | tail -1)" = "$GLIBC_FLOOR" ] ||
	die "libgs_sdk.so needs glibc $need, newer than $GLIBC_FLOOR: build it on an older system"
exports=$(objdump -T "$linux/lib/libgs_sdk.so" | awk '$4 == ".text" {print $7}' | grep -c '^gs_')
cp "$WORK/survivalservers-linux" "$linux/tools/survivalservers"
cp "$WORK/notices-linux.txt" "$linux/tools/THIRD_PARTY_NOTICES.txt"
sed -e "s/@VERSION@/$VERSION/g" -e "s/@SYSTEM@/Linux x64/g" -e "s/@GLIBC@/$need/g" "$HERE/README.archive.md" > "$linux/README.md"
no_stand_ins "$linux"
tar --owner=0 --group=0 --sort=name -C "$WORK" -czf "$OUT/$NAME-linux-x64.tar.gz" "$NAME-linux-x64"
echo "linux: libgs_sdk.so exports $exports gs_ functions, needs glibc $need or newer"

# Windows x64: the libraries Microsoft's compiler made, beside the same parts.
if [ -n "$WIN" ]; then
	windows=$WORK/$NAME-windows-x64
	cp -r "$common" "$windows"
	mkdir -p "$windows/lib"
	for f in gs_sdk.dll gs_sdk.lib gs_sdk.pdb gs_sdk_static.lib; do [ ! -e "$WIN/$f" ] || cp "$WIN/$f" "$windows/lib/"; done
	cp "$WORK/survivalservers-windows.exe" "$windows/tools/survivalservers.exe"
	cp "$WORK/notices-windows.txt" "$windows/tools/THIRD_PARTY_NOTICES.txt"
	sed -e "s/@VERSION@/$VERSION/g" -e "s/@SYSTEM@/Windows x64/g" -e "s/@GLIBC@/$need/g" "$HERE/README.archive.md" > "$windows/README.md"
	no_stand_ins "$windows"
	python3 -I - "$WORK" "$NAME-windows-x64" "$OUT/$NAME-windows-x64.zip" <<'EOF'
import os, sys, zipfile
work, name, out = sys.argv[1:]
with zipfile.ZipFile(out, 'w', zipfile.ZIP_DEFLATED) as z:
    for root, dirs, files in os.walk(os.path.join(work, name)):
        dirs.sort()
        for f in sorted(files):
            path = os.path.join(root, f)
            z.write(path, os.path.relpath(path, work))
EOF
	echo "windows: $(ls "$windows/lib" | tr '\n' ' ')"
fi

(cd "$OUT" && sha256sum $(ls "$NAME"-*) > SHA256SUMS)
rm -rf "$WORK"
cat "$OUT/SHA256SUMS"
