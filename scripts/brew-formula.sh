#!/usr/bin/env bash
# Prints the Homebrew formula for a release: brew-formula.sh VERSION CHECKSUMS
# The caveats are the post-update notice (internal/update/notice.txt), which
# brew shows after install and upgrade: the people who only run CKB through an
# MCP client never see the notice in their terminal.
set -euo pipefail
VERSION="$1"
CHECKSUMS="$2"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BASE="https://github.com/SimplyLiz/CodeMCP/releases/download/v${VERSION}"
sha() {
  local s
  s=$(grep " $1\$" "$CHECKSUMS" | awk '{print $1}')
  [ -n "$s" ] || { echo "no checksum for $1" >&2; exit 1; }
  echo "$s"
}
DA=$(sha "ckb_${VERSION}_darwin_arm64.tar.gz")
DI=$(sha "ckb_${VERSION}_darwin_amd64.tar.gz")
LA=$(sha "ckb_${VERSION}_linux_arm64.tar.gz")
LI=$(sha "ckb_${VERSION}_linux_amd64.tar.gz")

cat <<EOF
class Ckb < Formula
  desc "Code intelligence orchestration layer for AI-assisted development"
  homepage "https://github.com/SimplyLiz/CodeMCP"
  version "${VERSION}"

  on_macos do
    on_arm do
      url "${BASE}/ckb_${VERSION}_darwin_arm64.tar.gz"
      sha256 "${DA}"
    end
    on_intel do
      url "${BASE}/ckb_${VERSION}_darwin_amd64.tar.gz"
      sha256 "${DI}"
    end
  end

  on_linux do
    on_arm do
      url "${BASE}/ckb_${VERSION}_linux_arm64.tar.gz"
      sha256 "${LA}"
    end
    on_intel do
      url "${BASE}/ckb_${VERSION}_linux_amd64.tar.gz"
      sha256 "${LI}"
    end
  end

  def install
    bin.install "ckb"
  end

  def caveats
    <<~EOS
EOF
# The text is data: sed copies it, so the shell never expands it (it holds
# "$25k"). Ruby would interpolate #{} and backslashes: a test keeps them out.
REF="brew-${VERSION%.*}"
sed -e "s/{{ref}}/${REF}/" -e '/./s/^/      /' "$ROOT/internal/update/notice.txt"
cat <<EOF
    EOS
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/ckb version")
  end
end
EOF
