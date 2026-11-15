#!/bin/sh
# deploy-local: make a commit of CKB the ckb this machine runs.
#
#   scripts/deploy-local.sh [rev]     (make deploy, rev defaults to HEAD)
#
# bin/ckb is the working build: any session rebuilds it from whatever the
# working tree holds at the time. The ckb on PATH, which MCP servers, hooks
# and other repos start, changes only here: built from the committed tree of
# rev (git archive, so uncommitted edits never get in), stamped with its
# commit, kept as $CKB_DEPLOY_DIR/ckb-<commit>, and swapped in by renaming
# the symlink. A running `ckb mcp` keeps the binary it started with.
set -eu

rev=${1:-HEAD}
dest=${CKB_DEPLOY_DIR:-$HOME/.local/share/ckb}
link=${CKB_DEPLOY_LINK:-$HOME/.local/bin/ckb}
keep=${CKB_DEPLOY_KEEP:-3}

commit=$(git rev-parse --short=7 "$rev^{commit}")
if [ -n "$(git status --porcelain --untracked-files=no)" ]; then
	echo "note: the working tree has uncommitted changes; deploying $commit without them"
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
git archive "$commit" | tar -x -C "$tmp"

version=$(sed -n 's/^[[:space:]]*Version = "\(.*\)"$/\1/p' "$tmp/internal/version/version.go")
pkg=github.com/SimplyLiz/CodeMCP/internal/version
mkdir -p "$dest" "$(dirname "$link")"
out="$dest/ckb-$commit"
(
	cd "$tmp"
	CGO_ENABLED=1 go build -trimpath -ldflags "-X $pkg.Version=$version -X $pkg.Commit=$commit -X $pkg.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)" -o "$out.new" ./cmd/ckb
)
"$out.new" version >/dev/null
mv -f "$out.new" "$out"

ln -s "$out" "$link.new.$$"
mv -f "$link.new.$$" "$link"

# Older deploys beyond the newest $keep go; the one just linked is the newest.
ls -t "$dest"/ckb-* 2>/dev/null | tail -n +"$((keep + 1))" | while read -r old; do rm -f "$old"; done

echo "deployed ckb $version ($commit) -> $link"
