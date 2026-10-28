#!/bin/bash
# Build the release binaries locally the way CI does (.github/workflows/ci.yml, job "release") and
# compare them with the SHA256SUMS that CI made. Exit 0 only if every hash matches.
#   tools/reproduce.sh vX.Y.Z dist/sign/SHA256SUMS
set -euo pipefail

TAG=${1:?usage: reproduce.sh <tag> <SHA256SUMS>}
SUMS=${2:?usage: reproduce.sh <tag> <SHA256SUMS>}
die() { echo "reproduce: $*" >&2; exit 1; }

# CI uses setup-go with go-version-file: go.mod, so the toolchain is exactly the go line.
want=go$(awk '$1 == "go" {print $2}' go.mod)
have=$(GOTOOLCHAIN=local go env GOVERSION)
if [ "$have" != "$want" ]; then
	# A distro Go (e.g. Fedora's go1.26.10-X:nodwarf5) builds different bytes. Use the official
	# toolchain of that version instead: the go command downloads it to the module cache.
	root=$(GOTOOLCHAIN=$want go env GOROOT) || die "local Go is $have and $want could not be fetched"
	export PATH="$root/bin:$PATH"
	have=$(GOTOOLCHAIN=local go env GOVERSION)
	[ "$have" = "$want" ] || die "local Go is $have, go.mod and CI use $want; install $want and retry"
	echo "reproduce: using the official $want from $root"
fi

# CI builds the tag, and Go stamps the VCS state into the binary.
[ "$(git rev-parse HEAD)" = "$(git rev-parse "$TAG^{commit}")" ] || die "checkout $TAG first (HEAD is not the tag)"
[ -z "$(git status --porcelain)" ] || die "working tree is not clean"

build() { # dir
	mkdir -p "$1"
	export CGO_ENABLED=0 GOTOOLCHAIN=local
	GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.version=$TAG" -o "$1/gobill-linux-amd64" ./cmd/gobill
	GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w -X main.version=$TAG" -o "$1/gobill-linux-arm64" ./cmd/gobill
	GOOS=linux GOARCH=arm GOARM=7 go build -trimpath -ldflags "-s -w -X main.version=$TAG" -o "$1/gobill-linux-armv7" ./cmd/gobill
	(cd "$1" && sha256sum gobill-linux-* > SHA256SUMS)
}

out=$(mktemp -d)
trap 'rm -rf "$out"' EXIT
build "$out/a"
build "$out/b"
diff "$out/a/SHA256SUMS" "$out/b/SHA256SUMS" || die "two local builds differ: the build is not reproducible, refusing to sign"
diff "$out/a/SHA256SUMS" "$SUMS" || die "CI SHA256SUMS differs from the local build (diff above, local vs CI): NOT signing"
echo "reproduce: local builds match CI SHA256SUMS"
