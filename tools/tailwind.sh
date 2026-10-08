#!/bin/sh
# Builds web/static/app.css with the pinned standalone Tailwind CLI (no Node needed).
set -eu
VERSION=4.1.11
cd "$(dirname "$0")/.."
case "$(uname -s)-$(uname -m)" in
  Linux-x86_64) asset=linux-x64 ;;
  Linux-aarch64|Linux-arm64) asset=linux-arm64 ;;
  Darwin-arm64) asset=macos-arm64 ;;
  Darwin-x86_64) asset=macos-x64 ;;
  *) echo "unsupported platform $(uname -s)-$(uname -m)" >&2; exit 1 ;;
esac
bin="bin/tailwindcss-$VERSION"
if [ ! -x "$bin" ]; then
  mkdir -p bin
  curl -fsSL -o "$bin" "https://github.com/tailwindlabs/tailwindcss/releases/download/v$VERSION/tailwindcss-$asset"
  chmod +x "$bin"
fi
exec "$bin" -i web/tailwind.css -o web/static/app.css --minify
