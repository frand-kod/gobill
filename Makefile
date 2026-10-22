VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o gobill ./cmd/gobill

test:
	go vet ./...
	go test ./...

css:
	sh tools/tailwind.sh

# Sign a draft release made by CI and publish it. The private key stays on this machine; see SECURITY.md.
# Run it from a clean checkout of the tag with the Go version of go.mod: it rebuilds the binaries
# and signs only if their hashes equal CI's SHA256SUMS.
#   make sign TAG=v0.1.18 [RELEASE_KEY=~/.config/gobill/release.key]
RELEASE_KEY ?= $(HOME)/.config/gobill/release.key
sign:
	@test -n "$(TAG)" || { echo "usage: make sign TAG=vX.Y.Z"; exit 1; }
	rm -rf dist/sign && mkdir -p dist/sign
	gh release download "$(TAG)" --pattern SHA256SUMS --dir dist/sign
	bash tools/reproduce.sh "$(TAG)" dist/sign/SHA256SUMS
	go run ./tools/releasesign sign "$(RELEASE_KEY)" dist/sign/SHA256SUMS
	go run ./tools/releasesign verify dist/sign/SHA256SUMS
	gh release upload "$(TAG)" dist/sign/SHA256SUMS.sig --clobber
	gh release edit "$(TAG)" --draft=false --latest

.PHONY: build test css sign
