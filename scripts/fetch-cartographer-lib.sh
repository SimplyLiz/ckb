#!/usr/bin/env bash
#
# Fetch the prebuilt CodeCartographer C-ABI static library + header for a target
# and stage them where the cgo directives in internal/cartographer/bridge.go
# expect (third_party/cartographer/{target/release,include}). The .a is already
# tree-sitter-localized upstream, so there is nothing to post-process — and no
# Rust toolchain is needed to build CKB with the cartographer tier.
#
# The library is pinned to a specific CodeCartographer release and verified
# against that release's sha256 checksums.txt; a tampered or truncated download
# fails the build instead of linking silently.
#
# Usage:
#   scripts/fetch-cartographer-lib.sh [GOOS] [GOARCH]
# Defaults to the host `go env GOOS/GOARCH`. Env overrides:
#   CARTOGRAPHER_VERSION (default below)   CARTOGRAPHER_REPO (default below)
#
# Exit codes: 0 = library staged (or already current); 2 = no prebuilt library
# for this target (e.g. windows) — caller should fall back to the stub build.

set -euo pipefail

CARTOGRAPHER_VERSION="${CARTOGRAPHER_VERSION:-v4.0.3}"
CARTOGRAPHER_REPO="${CARTOGRAPHER_REPO:-SimplyLiz/CodeCartographer}"

GOOS_IN="${1:-}"
GOARCH_IN="${2:-}"
GOOS="${GOOS_IN:-$(go env GOOS)}"
GOARCH="${GOARCH_IN:-$(go env GOARCH)}"

case "${GOOS}/${GOARCH}" in
  darwin/arm64) TRIPLE="aarch64-apple-darwin" ;;
  darwin/amd64) TRIPLE="x86_64-apple-darwin" ;;
  linux/amd64)  TRIPLE="x86_64-unknown-linux-gnu" ;;
  linux/arm64)  TRIPLE="aarch64-unknown-linux-gnu" ;;
  *)
    echo "cartographer: no prebuilt library for ${GOOS}/${GOARCH} (${CARTOGRAPHER_VERSION}) — build the stub tier instead" >&2
    exit 2
    ;;
esac

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEST_DIR="${SCRIPT_DIR}/../third_party/cartographer"
LIB_DIR="${DEST_DIR}/target/release"
INC_DIR="${DEST_DIR}/include"
STAMP="${DEST_DIR}/.fetched-lib"           # records "<version> <triple>" of the staged lib

TARBALL="codecartographer-${TRIPLE}.tar.gz"
BASE_URL="https://github.com/${CARTOGRAPHER_REPO}/releases/download/${CARTOGRAPHER_VERSION}"

# Already staged for this exact version+target? Skip the network round-trip.
if [[ -f "${LIB_DIR}/libcode_cartographer.a" && -f "${INC_DIR}/codecartographer.h" \
      && -f "${STAMP}" && "$(cat "${STAMP}" 2>/dev/null)" == "${CARTOGRAPHER_VERSION} ${TRIPLE}" ]]; then
  echo "cartographer: ${CARTOGRAPHER_VERSION} ${TRIPLE} already staged"
  exit 0
fi

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'
  else shasum -a 256 "$1" | awk '{print $1}'; fi
}

# Prefer `gh release download` — it uses the caller's auth, so it works whether
# CodeCartographer is public or private (a private repo's asset URLs 404 for
# unauthenticated curl). Fall back to curl/wget against the public asset URL when
# gh is unavailable or unauthenticated.
gh_available() { command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; }

curl_fetch() {
  # $1 url, $2 dest
  if command -v curl >/dev/null 2>&1; then curl -fsSL "$1" -o "$2"
  elif command -v wget >/dev/null 2>&1; then wget -qO "$2" "$1"
  else echo "cartographer: need gh, curl, or wget to fetch the library" >&2; exit 1; fi
}

WORK="$(mktemp -d)"
trap 'rm -rf "${WORK}"' EXIT

echo "cartographer: fetching ${TARBALL} @ ${CARTOGRAPHER_VERSION}"
if gh_available; then
  gh release download "${CARTOGRAPHER_VERSION}" --repo "${CARTOGRAPHER_REPO}" \
    --pattern "${TARBALL}" --pattern "checksums.txt" --dir "${WORK}"
else
  curl_fetch "${BASE_URL}/${TARBALL}" "${WORK}/${TARBALL}"
  curl_fetch "${BASE_URL}/checksums.txt" "${WORK}/checksums.txt"
fi

EXPECTED="$(awk -v f="${TARBALL}" '$2 == f {print $1}' "${WORK}/checksums.txt")"
if [[ -z "${EXPECTED}" ]]; then
  echo "cartographer: ${TARBALL} not listed in checksums.txt — aborting" >&2
  exit 1
fi
ACTUAL="$(sha256 "${WORK}/${TARBALL}")"
if [[ "${EXPECTED}" != "${ACTUAL}" ]]; then
  echo "cartographer: checksum mismatch for ${TARBALL}" >&2
  echo "  expected ${EXPECTED}" >&2
  echo "  actual   ${ACTUAL}" >&2
  exit 1
fi

tar -xzf "${WORK}/${TARBALL}" -C "${WORK}"
EXTRACT="${WORK}/codecartographer-${TRIPLE}"

mkdir -p "${LIB_DIR}" "${INC_DIR}"
cp "${EXTRACT}/lib/libcode_cartographer.a" "${LIB_DIR}/libcode_cartographer.a"
cp "${EXTRACT}/include/codecartographer.h" "${INC_DIR}/codecartographer.h"
echo "${CARTOGRAPHER_VERSION} ${TRIPLE}" > "${STAMP}"

echo "cartographer: staged ${CARTOGRAPHER_VERSION} ${TRIPLE} (sha256 ${ACTUAL:0:12}…)"
echo "  lib: ${LIB_DIR}/libcode_cartographer.a"
echo "  hdr: ${INC_DIR}/codecartographer.h"
