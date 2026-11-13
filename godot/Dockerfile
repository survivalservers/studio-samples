# The Linux image: Godot's own headless program running this project. Godot does not catch SIGTERM, so the
# version stops it through the local API ("stop": {"method": "local_api"}), which server.gd waits for.
#   docker build -t godot-server .
FROM debian:bookworm-slim
ARG GODOT_VERSION=4.7.2
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl unzip \
 && curl -fsSL -o /tmp/godot.zip "https://github.com/godotengine/godot/releases/download/${GODOT_VERSION}-stable/Godot_v${GODOT_VERSION}-stable_linux.x86_64.zip" \
 && curl -fsSL -o /tmp/godot-sums.txt "https://github.com/godotengine/godot/releases/download/${GODOT_VERSION}-stable/SHA512-SUMS.txt" \
 && echo "$(grep " Godot_v${GODOT_VERSION}-stable_linux.x86_64.zip\$" /tmp/godot-sums.txt | cut -d' ' -f1)  /tmp/godot.zip" | sha512sum -c - \
 && unzip -q /tmp/godot.zip -d /tmp/godot \
 && mv /tmp/godot/Godot_v${GODOT_VERSION}-stable_linux.x86_64 /usr/local/bin/godot \
 && rm -rf /tmp/godot /tmp/godot.zip /tmp/godot-sums.txt \
 && apt-get purge -y curl unzip && apt-get autoremove -y && rm -rf /var/lib/apt/lists/*
COPY project.godot server.tscn server.gd /game/
# Build Godot's cache now, so a start does not spend time on it.
RUN godot --headless --path /game --import
ENV HOME=/tmp
USER nobody
EXPOSE 7777/udp
ENTRYPOINT ["godot", "--headless", "--path", "/game"]
