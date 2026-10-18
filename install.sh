#!/bin/sh
# Install ctm-ops (aws-dev-ops-utils) from a GitHub release.
#
#   curl -fsSL https://raw.githubusercontent.com/moovendhan-v/aws-cli-tui/main/install.sh | sh
#
# Options (environment variables):
#   VERSION=0.1.0              install a specific version (default: latest)
#   INSTALL_DIR=/usr/local/bin where to put ctm-ops (default: /usr/local/bin if
#                              writable, otherwise ~/.local/bin)
set -eu

REPO="moovendhan-v/aws-cli-tui"

say() { printf '%s\n' "$*"; }
fail() { printf 'install: %s\n' "$*" >&2; exit 1; }

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  darwin | linux) ;;
  *) fail "unsupported OS: $os (macOS and Linux only)" ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) fail "unsupported CPU: $(uname -m)" ;;
esac

version="${VERSION:-}"
if [ -z "$version" ]; then
  version=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" |
    sed -n 's/.*"tag_name": *"v\{0,1\}\([^"]*\)".*/\1/p' | head -n 1)
  [ -n "$version" ] || fail "could not find the latest release of $REPO"
fi
version="${version#v}"

file="aws-dev-ops-utils_${version}_${os}_${arch}.tar.gz"
base="${BASE_URL:-https://github.com/$REPO/releases/download/v$version}"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

say "Downloading ctm-ops $version for $os/$arch…"
curl -fsSL -o "$tmp/$file" "$base/$file" || fail "download failed: $base/$file"
curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt" || fail "could not download checksums"

expected=$(awk -v f="$file" '$2 == f { print $1 }' "$tmp/checksums.txt")
[ -n "$expected" ] || fail "no checksum listed for $file"
if command -v shasum >/dev/null 2>&1; then
  actual=$(shasum -a 256 "$tmp/$file" | awk '{ print $1 }')
else
  actual=$(sha256sum "$tmp/$file" | awk '{ print $1 }')
fi
[ "$expected" = "$actual" ] || fail "checksum mismatch for $file"

tar -xzf "$tmp/$file" -C "$tmp" ctm-ops

dir="${INSTALL_DIR:-}"
if [ -z "$dir" ]; then
  if [ -w /usr/local/bin ]; then dir=/usr/local/bin; else dir="$HOME/.local/bin"; fi
fi
mkdir -p "$dir"
install -m 0755 "$tmp/ctm-ops" "$dir/ctm-ops"

say "Installed ctm-ops $version to $dir/ctm-ops"
case ":$PATH:" in
  *":$dir:"*) ;;
  *) say "Add $dir to your PATH, e.g.:  echo 'export PATH=\"$dir:\$PATH\"' >> ~/.zshrc" ;;
esac
say "Run it with:  ctm-ops"
say "For SSM sessions / ECS Exec also install the AWS CLI and the Session Manager plugin."
