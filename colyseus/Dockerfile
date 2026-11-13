# The Linux image: the bundled server on Node's slim image, running as its non-root user.
#   npm ci && npm run build && docker build -t colyseus-server .
# The image listens on 2567 (TCP, WebSocket); the platform maps each game server's public port onto it
# (container_port: 2567).
FROM node:26-bookworm-slim
WORKDIR /app
COPY dist/server.cjs /app/server.cjs
USER node
EXPOSE 2567/tcp
ENTRYPOINT ["node", "/app/server.cjs"]
