#!/usr/bin/env bash
# Builds the sample for both systems from one source:
#   dist/server.cjs and dist/client.cjs   the server and a scripted player, each one file (npm run build)
#   dist/colyseus-server-windows.zip      upload as a Windows build: Node's own node.exe beside server.cjs; its
#                                         executable is node.exe and its argument server.cjs (version-windows.json)
# Needs Node 22 or newer and Python 3 (for the zip). With Docker installed it also builds the image colyseus-server:dev.
set -euo pipefail
cd "$(dirname "$0")"
# The Node that runs the Windows build, checked against nodejs.org/dist/v26.13.0/SHASUMS256.txt.
NODE_VERSION=26.13.0
NODE_WIN_SHA256=dcc60d59a0746999f99064a01c020445d54d84228c691cdbf25b49ac3b59150a

npm ci --no-audit --no-fund
npm run build

cache=${XDG_CACHE_HOME:-$HOME/.cache}/colyseus-sample
node_zip=$cache/node-v$NODE_VERSION-win-x64.zip
mkdir -p "$cache"
if [ ! -f "$node_zip" ]; then
	curl -fsSL -o "$node_zip.part" "https://nodejs.org/dist/v$NODE_VERSION/node-v$NODE_VERSION-win-x64.zip"
	mv "$node_zip.part" "$node_zip"
fi
echo "$NODE_WIN_SHA256  $node_zip" | sha256sum -c --quiet -
python3 - "$node_zip" "node-v$NODE_VERSION-win-x64" <<'PY'
import sys, zipfile
src, top = sys.argv[1], sys.argv[2]
with zipfile.ZipFile(src) as z, zipfile.ZipFile("dist/colyseus-server-windows.zip", "w", zipfile.ZIP_DEFLATED) as out:
    out.writestr("node.exe", z.read(f"{top}/node.exe"))
    out.writestr("NODE-LICENSE.txt", z.read(f"{top}/LICENSE"))
    out.write("dist/server.cjs", "server.cjs")
PY
if command -v docker >/dev/null 2>&1; then
	docker build -t colyseus-server:dev .
else
	echo "Docker not found: skipped the image (dist/server.cjs is ready for it)"
fi
ls -l dist/colyseus-server-windows.zip dist/server.cjs dist/client.cjs
