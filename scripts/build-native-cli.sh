#!/usr/bin/env bash
set -euo pipefail
[ "$(uname -s)" = Darwin ] || { echo 'native CLI build requires macOS' >&2; exit 1; }
[ "$#" = 2 ] || { echo 'usage: build-native-cli.sh amd64|arm64 output-directory' >&2; exit 1; }
ARCH="$1"
case "$ARCH" in amd64|arm64) ;; *) echo 'unsupported native architecture' >&2; exit 1;; esac
[ "$(go env GOHOSTARCH)" = "$ARCH" ] || { echo 'native CLI build requires matching host architecture' >&2; exit 1; }
OUT="$2"
VERSION="${CLI_RELEASE_VERSION:-snapshot}"
[[ "$VERSION" =~ ^[A-Za-z0-9._-]+$ ]] || { echo 'invalid release version' >&2; exit 1; }
WORK=$(mktemp -d)
cleanup() { python3 - "$WORK" <<'PY'
import shutil,sys
shutil.rmtree(sys.argv[1])
PY
}
trap cleanup EXIT
CGO_ENABLED=1 go test ./internal/passoauth -count=1
CGO_ENABLED=1 go build -ldflags="-s -w -X github.com/pyxcloud/pyxcloud-cli/cmd.Version=$VERSION" -o "$WORK/pyxcloud" .
CGO_ENABLED=1 go build -o "$WORK/passo" ./cmd/passo
# Ad-hoc signatures verify local binary integrity; they are not Developer ID
# signatures or Apple notarization and must never be represented as such.
codesign --force --sign - --identifier io.pyxcloud.passo "$WORK/passo"
codesign --force --sign - --identifier io.pyxcloud.cli "$WORK/pyxcloud"
codesign --verify --strict "$WORK/passo"
codesign --verify --strict "$WORK/pyxcloud"
otool -L "$WORK/passo" | grep -q '/Security.framework/' || { echo 'native Keychain framework missing' >&2; exit 1; }
"$WORK/passo" --help >/dev/null
mkdir -p "$OUT"
LABEL=arm64
[ "$ARCH" != amd64 ] || LABEL=x86_64
tar -czf "$OUT/pyxcloud_Darwin_$LABEL.tar.gz" -C "$WORK" pyxcloud passo
