VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o nuxbill ./cmd/nuxbill

test:
	go vet ./...
	go test ./...

css:
	sh tools/tailwind.sh

.PHONY: build test css
