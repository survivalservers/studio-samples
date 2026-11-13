# The Linux image: the headless server launcher from build.sh on Ubuntu, as a non-root user. The SDK's libraries link
# X11, XCB, xkbcommon, libunwind and zstd though the server opens no window, so they are installed.
#   O3DE=/opt/O3DE/26.05 ./build.sh && docker build -t o3de-server .
# The image listens on 7777; the platform maps each game server's public port onto it (container_port: 7777).
# ca-certificates: the base image has none, and a game server's HTTPS calls (sign-in, telemetry, a studio's own
# backend) fail without it (Unity's TLS in Unturned's server did).
FROM ubuntu:24.04
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates libatomic1 libunwind8 libx11-6 libxcb1 libxcb-keysyms1 \
	libxcb-randr0 libxcb-xfixes0 libxcb-xinput0 libxcb-xkb1 libxkbcommon0 libxkbcommon-x11-0 libzstd1 \
	&& rm -rf /var/lib/apt/lists/*
COPY dist/linux /game
WORKDIR /game/bin
USER 1000:1000
EXPOSE 7777/udp
ENTRYPOINT ["/game/bin/LabServer.HeadlessServerLauncher", "--project-path=/game/project", "-rhi=null", "-bg_ConnectToAssetProcessor=0"]
