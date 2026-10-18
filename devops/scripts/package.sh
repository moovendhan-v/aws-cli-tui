#!/usr/bin/env bash
# Build release archives for macOS and Linux (arm64 + amd64).
# Each archive holds the ctm-ops binary and the README.
# Usage: scripts/package.sh <version> [out-dir]
set -euo pipefail
version="${1:?usage: package.sh <version> [out-dir]}"
out="${2:-dist}"
cd "$(dirname "$0")/.."
rm -rf "$out" && mkdir -p "$out"
for target in darwin/arm64 darwin/amd64 linux/arm64 linux/amd64; do
  os="${target%/*}" arch="${target#*/}"
  name="aws-dev-ops-utils_${version}_${os}_${arch}"
  mkdir -p "$out/$name"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath \
    -ldflags "-s -w -X main.version=${version}" -o "$out/$name/ctm-ops" ./cmd/ctm-ops
  cp README.md "$out/$name/"
  tar -C "$out/$name" -czf "$out/$name.tar.gz" ctm-ops README.md
  rm -rf "$out/$name"
done
(cd "$out" && shasum -a 256 *.tar.gz > checksums.txt)
echo "archives in $out:" && cat "$out/checksums.txt"
