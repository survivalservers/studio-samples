#!/usr/bin/env bash
# Builds (cooks) the sample with the Flax editor run headless, with no window or graphics card:
#   Output/Linux/      the game for Linux (FlaxServer and its data, with its own .NET runtime); the Dockerfile packs it
#   Output/Windows/    the game for Windows, when built on Windows (FlaxEditor.exe ... -build Server.Windows)
#   dist/flax-server-windows.zip   that folder as a zip to upload; its executable is FlaxServer.exe
# Needs FLAX_EDITOR (the editor program, 1.13, with its Linux platform tools in Source/Platforms/Linux) and the
# .NET 10 SDK. Build presets: Content/Settings/Build Settings.json, preset "Server".
set -euo pipefail
cd "$(dirname "$0")"
: "${FLAX_EDITOR:?set FLAX_EDITOR to the Flax editor program}"
target=${1:-Linux}
# -project=PATH: the editor reads its options from the joined command line, where a value starting with "/" reads
# as another option.
"$FLAX_EDITOR" -headless -null -mute -std -project="$PWD" -build "Server.$target"
if [ "$target" = Windows ]; then
	mkdir -p dist
	python3 -c 'import os, sys, zipfile
root = "Output/Windows"
with zipfile.ZipFile("dist/flax-server-windows.zip", "w", zipfile.ZIP_DEFLATED) as z:
    for d, _, files in os.walk(root):
        for f in sorted(files):
            z.write(os.path.join(d, f), os.path.relpath(os.path.join(d, f), root))'
fi
ls -l "Output/$target"
