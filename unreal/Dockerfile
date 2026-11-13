# The Linux image for the packaged Linux server (Builds/LinuxServer, from build-server.bat).
#   docker build -t unreal-server .
# Unreal servers refuse to run as root, so the image has its own user. The server listens on 7777; the platform maps
# each game server's public port onto it (container_port: 7777).
FROM ubuntu:24.04
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/* \
 && useradd --create-home --uid 10001 server
COPY --chown=server Builds/LinuxServer/ /game/
# A server packaged on Windows has no execute bits: give them to the script and the program.
RUN chmod +x /game/GameServerSampleServer.sh /game/GameServerSample/Binaries/Linux/GameServerSampleServer-Linux-Shipping
USER server
EXPOSE 7777/udp
ENTRYPOINT ["/game/GameServerSampleServer.sh", "-log", "-Port=7777"]
