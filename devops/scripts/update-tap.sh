#!/usr/bin/env bash
# Write Formula/aws-dev-ops-utils.rb in moovendhan-v/homebrew-tap for a
# published release, using your GitHub CLI login (no extra token needed).
# Usage: scripts/update-tap.sh <version>      e.g. scripts/update-tap.sh 0.1.0
set -euo pipefail
version="${1:?usage: update-tap.sh <version>}"
version="${version#v}"
repo="moovendhan-v/aws-dev-ops-utils"
tap="moovendhan-v/homebrew-tap"
path="Formula/aws-dev-ops-utils.rb"
cd "$(dirname "$0")"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

gh release download "v${version}" --repo "$repo" --pattern checksums.txt --dir "$tmp"
./formula.sh "$version" "$tmp/checksums.txt" > "$tmp/formula.rb"
ruby -c "$tmp/formula.rb" >/dev/null

# Update in place if the formula already exists (the API needs its blob SHA).
sha=$(gh api "repos/$tap/contents/$path" --jq .sha 2>/dev/null || true)
args=(-X PUT "repos/$tap/contents/$path" -f "message=aws-dev-ops-utils ${version}" -f "content=$(base64 < "$tmp/formula.rb" | tr -d '\n')")
[ -n "$sha" ] && args+=(-f "sha=$sha")
gh api "${args[@]}" --jq '.commit.html_url'
echo "Tap updated. Install with: brew install $tap/aws-dev-ops-utils" | sed 's#homebrew-tap#tap#'
