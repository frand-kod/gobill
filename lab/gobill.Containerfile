# gobill + lab seeder on Alpine. The repo Dockerfile is distroless (no shell, no wget), so it cannot
# run a podman healthcheck; the binary is the same one it builds.
FROM docker.io/library/alpine:3.22
COPY gobill /usr/local/bin/gobill
COPY seed /usr/local/bin/lab-seed
ENV GOBILL_DB=/data/gobill.db GOBILL_HTTP=:8080 GOBILL_RADIUS=:1812 GOBILL_HTTPS=0 GOBILL_BACKUP_DIR=/data/backup
VOLUME ["/data"]
ENTRYPOINT ["/usr/local/bin/gobill"]
