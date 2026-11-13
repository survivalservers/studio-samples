#!/usr/bin/env bash
# Builds the sample game server for both systems from one source:
#   dist/plain-server-windows.zip   upload as a Windows build; its executable is PlainServer.exe
#   dist/linux/plain-server         the Linux program, which the Dockerfile packs into an image
# Needs Go 1.22 or newer. With Docker installed it also builds the image plain-server:dev.
set -euo pipefail
cd "$(dirname "$0")"
rm -rf dist && mkdir -p dist/windows dist/linux
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags=-s -o dist/windows/PlainServer.exe .
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags=-s -o dist/linux/plain-server .
(cd dist/windows && zip -q -X ../plain-server-windows.zip PlainServer.exe)
if command -v docker >/dev/null 2>&1; then
	docker build -t plain-server:dev .
else
	echo "Docker not found: skipped the image (dist/linux/plain-server is ready for it)"
fi
ls -l dist/plain-server-windows.zip dist/linux/plain-server
