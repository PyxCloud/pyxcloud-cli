#!/usr/bin/env bash
# Vendor the pyx-backend OpenAPI contracts into api/contracts/.
#
# Usage:
#   scripts/sync-contracts.sh              # re-sync at the SHA pinned in api/contracts/SOURCE
#   scripts/sync-contracts.sh --ref main   # resolve a branch/tag/SHA, sync it, re-pin SOURCE
#
# Every contracts/*.openapi.json file at the ref is copied verbatim; vendored
# files that no longer exist upstream are removed, so api/contracts/ mirrors
# the upstream directory exactly. After syncing, regenerate the client with
#   go generate ./internal/api/gen/...
#
# pyx-backend is private: a token with read access to it is required, taken
# from PYX_BACKEND_TOKEN, GH_TOKEN or GITHUB_TOKEN (in that order), falling
# back to `gh auth token`. The token is only sent to api.github.com.
#
# Requirements: bash, curl, jq.
set -euo pipefail

cd "$(dirname "$0")/.."
DEST="api/contracts"
SOURCE_FILE="$DEST/SOURCE"

die() { echo "sync-contracts: $*" >&2; exit 1; }

command -v curl >/dev/null || die "curl is required"
command -v jq >/dev/null || die "jq is required"
[ -f "$SOURCE_FILE" ] || die "$SOURCE_FILE not found"

src_get() { sed -n "s/^$1=//p" "$SOURCE_FILE" | head -n1; }
REPO="$(src_get repo)"
CPATH="$(src_get path)"
PINNED="$(src_get sha)"
[ -n "$REPO" ] && [ -n "$CPATH" ] && [ -n "$PINNED" ] || die "$SOURCE_FILE must define repo=, path= and sha="

REF="$PINNED"
while [ $# -gt 0 ]; do
  case "$1" in
    --ref) [ $# -ge 2 ] || die "--ref needs a value"; REF="$2"; shift 2 ;;
    -h|--help) sed -n '2,17p' "$0"; exit 0 ;;
    *) die "unknown argument: $1" ;;
  esac
done

TOKEN="${PYX_BACKEND_TOKEN:-${GH_TOKEN:-${GITHUB_TOKEN:-}}}"
if [ -z "$TOKEN" ] && command -v gh >/dev/null; then
  TOKEN="$(gh auth token 2>/dev/null || true)"
fi
[ -n "$TOKEN" ] || die "no GitHub token: set PYX_BACKEND_TOKEN (read access to $REPO)"

API="https://api.github.com/repos/$REPO"
gh_get() { # gh_get <accept> <url>
  curl -fsSL --retry 3 \
    -H "Authorization: Bearer $TOKEN" \
    -H "Accept: $1" \
    -H "X-GitHub-Api-Version: 2022-11-28" \
    "$2"
}

# Resolve the ref to a full commit SHA so SOURCE always records an immutable pin.
SHA="$(gh_get application/vnd.github+json "$API/commits/$REF" | jq -er .sha)" \
  || die "cannot resolve ref '$REF' in $REPO"

LISTING="$(gh_get application/vnd.github+json "$API/contents/$CPATH?ref=$SHA")" \
  || die "cannot list $CPATH at $SHA"
FILES="$(jq -r '.[] | select(.type=="file") | .name | select(endswith(".openapi.json"))' <<<"$LISTING" | LC_ALL=C sort)"
[ -n "$FILES" ] || die "no *.openapi.json under $CPATH at $SHA"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
for f in $FILES; do
  gh_get application/vnd.github.raw "$API/contents/$CPATH/$f?ref=$SHA" >"$TMP/$f" \
    || die "cannot fetch $CPATH/$f at $SHA"
  jq -e . "$TMP/$f" >/dev/null || die "$CPATH/$f at $SHA is not valid JSON"
done

rm -f "$DEST"/*.openapi.json
cp "$TMP"/*.openapi.json "$DEST"/

{
  echo "# Vendored pyx-backend OpenAPI contracts. Managed by scripts/sync-contracts.sh; do not edit by hand."
  echo "repo=$REPO"
  echo "path=$CPATH"
  echo "sha=$SHA"
} >"$SOURCE_FILE"

echo "sync-contracts: $(wc -w <<<"$FILES" | tr -d ' ') contracts from $REPO@$SHA (ref: $REF)"
for f in $FILES; do echo "  $f"; done
