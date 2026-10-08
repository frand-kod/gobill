# syntax=docker/dockerfile:1
# go.mod meminta Go 1.26.x, jadi builder mengikuti versi itu.
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/nuxbill ./cmd/nuxbill

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/nuxbill /usr/local/bin/nuxbill
ENV NUXBILL_DB=/data/nuxbill.db \
    NUXBILL_HTTP=:8080 \
    NUXBILL_RADIUS=:1812 \
    NUXBILL_BACKUP_DIR=/data/backup
# distroless nonroot memakai UID 65532.
USER nonroot:nonroot
VOLUME ["/data"]
EXPOSE 8080/tcp 1812/udp 1813/udp
ENTRYPOINT ["/usr/local/bin/nuxbill"]
