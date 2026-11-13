# The Linux image: the program from build.sh on a minimal base, running as a non-root user.
#   ./build.sh && docker build -t plain-server .
# The image listens on 7777; the platform maps each game server's public port onto it (container_port: 7777).
FROM gcr.io/distroless/static-debian12:nonroot
COPY dist/linux/plain-server /plain-server
EXPOSE 7777/udp
ENTRYPOINT ["/plain-server"]
