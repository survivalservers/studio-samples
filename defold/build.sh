#!/usr/bin/env bash
# Builds the sample with Defold's command-line builder (bob.jar) for both systems, as headless engines:
#   dist/linux/DefoldServer/      dmengine_headless as DefoldServer.x86_64 and the game's archive; the Dockerfile packs it
#   dist/defold-server-windows.zip   upload as a Windows build; its executable is DefoldServer.exe
# Needs Java 25 (JAVA, or java on the PATH) and Python 3 (for the zip). bob.jar is downloaded once and checked.
set -euo pipefail
cd "$(dirname "$0")"
DEFOLD_VERSION=1.14.0
BOB_SHA256=d8cbe667757ca1c5f4dfd251bc98660c35e7a65ac84bf0207ccef7607de6d1a1
JAVA=${JAVA:-java}

cache=${XDG_CACHE_HOME:-$HOME/.cache}/defold-sample
bob=$cache/bob-$DEFOLD_VERSION.jar
mkdir -p "$cache"
if [ ! -f "$bob" ]; then
	curl -fsSL -o "$bob.part" "https://github.com/defold/defold/releases/download/$DEFOLD_VERSION/bob.jar"
	mv "$bob.part" "$bob"
fi
echo "$BOB_SHA256  $bob" | sha256sum -c --quiet -

rm -rf dist build && mkdir -p dist/linux dist/windows
for platform in x86_64-linux x86_64-win32; do
	out=dist/linux
	[ "$platform" = x86_64-win32 ] && out=dist/windows
	"$JAVA" -jar "$bob" --archive --platform "$platform" --variant headless --bundle-output "$out" \
		resolve build bundle
done
python3 - <<'PY'
import os, zipfile
root = "dist/windows/DefoldServer"
with zipfile.ZipFile("dist/defold-server-windows.zip", "w", zipfile.ZIP_DEFLATED) as z:
    for d, _, files in os.walk(root):
        for f in sorted(files):
            p = os.path.join(d, f)
            z.write(p, os.path.relpath(p, root))
PY
ls -lR dist | head -30
