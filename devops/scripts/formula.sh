#!/usr/bin/env bash
# Print the Homebrew formula for a release.
# Usage: scripts/formula.sh <version> <checksums.txt> [base-url]
set -euo pipefail
version="${1:?version}" sums="${2:?checksums.txt}"
base="${3:-https://github.com/moovendhan-v/aws-dev-ops-utils/releases/download/v${version}}"
sha() { awk -v f="aws-dev-ops-utils_${version}_$1.tar.gz" '$2==f {print $1}' "$sums"; }
for t in darwin_arm64 darwin_amd64 linux_arm64 linux_amd64; do
  [ -n "$(sha "$t")" ] || { echo "missing checksum for $t" >&2; exit 1; }
done
cat <<RUBY
class AwsDevOpsUtils < Formula
  desc "Keyboard- and mouse-driven AWS DevOps terminal (EC2, ECS, logs, pipelines, S3)"
  homepage "https://github.com/moovendhan-v/aws-dev-ops-utils"
  version "${version}"

  on_macos do
    on_arm do
      url "${base}/aws-dev-ops-utils_${version}_darwin_arm64.tar.gz"
      sha256 "$(sha darwin_arm64)"
    end
    on_intel do
      url "${base}/aws-dev-ops-utils_${version}_darwin_amd64.tar.gz"
      sha256 "$(sha darwin_amd64)"
    end
  end

  on_linux do
    on_arm do
      url "${base}/aws-dev-ops-utils_${version}_linux_arm64.tar.gz"
      sha256 "$(sha linux_arm64)"
    end
    on_intel do
      url "${base}/aws-dev-ops-utils_${version}_linux_amd64.tar.gz"
      sha256 "$(sha linux_amd64)"
    end
  end

  def install
    bin.install "ctm-ops"
  end

  def caveats
    <<~EOS
      Run it with: ctm-ops

      It uses your existing AWS credentials and profiles (~/.aws).

      For SSM sessions, ECS Exec and database tunnels, also install:
        brew install awscli
        brew install --cask session-manager-plugin

      Optional: psql (brew install libpq) or mysql (brew install mysql-client)
      for SQL shells, and ffmpeg for S3 video previews.
    EOS
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/ctm-ops --version")
  end
end
RUBY
