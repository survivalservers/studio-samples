# The Linux image: the headless bundle from build.sh on a small Debian, running as a non-root user.
#   ./build.sh && docker build -t defold-server .
# Defold's headless engine never opens a window or a sound device, but it still links the X11 and OpenAL libraries,
# so they are installed. The image listens on 7777; the platform maps each game server's public port onto it
# (container_port: 7777).
# ca-certificates: the base image has none, and a game server's HTTPS calls (sign-in, telemetry, a studio's own
# backend) fail without it (Unity's TLS in Unturned's server did).
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates libx11-6 libxext6 libxi6 libopenal1 \
	&& rm -rf /var/lib/apt/lists/*
COPY dist/linux/DefoldServer /game
WORKDIR /game
USER 1000:1000
EXPOSE 7777/udp
ENTRYPOINT ["/game/DefoldServer.x86_64"]
