# The Linux image: the cooked game from build.sh on Ubuntu, run headless as a non-root user. The game carries its own
# .NET runtime. Flax's engine still links the X11, Wayland and GLib libraries though it opens no window, and .NET
# needs ICU, so they are installed.
#   ./build.sh && docker build -t flax-server .
# The image listens on 7777; the platform maps each game server's public port onto it (container_port: 7777).
# ca-certificates: the base image has none, and a game server's HTTPS calls (sign-in, telemetry, a studio's own
# backend) fail without it (Unity's TLS in Unturned's server did).
FROM ubuntu:24.04
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates libx11-6 libxcursor1 libxinerama1 libxfixes3 \
	libwayland-client0 libglib2.0-0t64 libicu74 && rm -rf /var/lib/apt/lists/*
COPY Output/Linux /game
WORKDIR /game
USER 1000:1000
EXPOSE 7777/udp
ENTRYPOINT ["/game/FlaxServer", "-headless", "-null", "-mute", "-std"]
