# The Linux image for a Unity dedicated server built with Game Server > Build Linux server (Builds/LinuxServer).
# Run from the Unity project folder:  docker build -f Packages/com.survivalservers.studio/Dockerfile -t unity-server .
# "-logFile -" sends Unity's log to the console, which the platform keeps.
FROM ubuntu:24.04
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
COPY Builds/LinuxServer/ /game/
RUN chmod +x /game/GameServer.x86_64
ENV HOME=/tmp
USER nobody
EXPOSE 7777/udp
ENTRYPOINT ["/game/GameServer.x86_64", "-batchmode", "-nographics", "-logFile", "-"]
