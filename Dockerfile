# syntax=docker/dockerfile:1
# go.mod meminta Go 1.26.x, jadi builder mengikuti versi itu.
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/gobill ./cmd/gobill

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/gobill /usr/local/bin/gobill
ENV GOBILL_DB=/data/gobill.db \
    GOBILL_HTTP=:8080 \
    GOBILL_RADIUS=:1812 \
    GOBILL_BACKUP_DIR=/data/backup
# distroless nonroot memakai UID 65532.
USER nonroot:nonroot
VOLUME ["/data"]
EXPOSE 8080/tcp 1812/udp 1813/udp
ENTRYPOINT ["/usr/local/bin/gobill"]
