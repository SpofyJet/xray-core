#!/bin/bash
# Build the Spofy remnanode image from spofy/dist/xray-linux-amd64.
# Tag: spofy/remnanode:<node ver>-x<xray ver>-<rev>, e.g. spofy/remnanode:3.4.5-x26.11.9-spofy-6e396e7
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
BIN=$ROOT/spofy/dist/xray-linux-amd64
[ -x "$BIN" ] || { echo "build first: spofy/build.sh"; exit 1; }
sha256sum -c --status <(sed "s#spofy/dist/#$ROOT/spofy/dist/#" "$BIN.sha256") || { echo "sha256 mismatch"; exit 1; }
VER=$("$BIN" version | head -1)
case "$VER" in *-dirty*) [ "${ALLOW_DIRTY:-0}" = 1 ] || { echo "refusing dirty build: $VER"; exit 1; };; esac
XV=$(echo "$VER" | awk '{print $2}'); REV=$(echo "$VER" | grep -o 'spofy-[0-9a-f]*\(-dirty\)\?')
TAG=spofy/remnanode:3.4.5-x$XV-$REV
CTX=$(mktemp -d); trap 'rm -rf "$CTX"' EXIT
cp "$BIN" "$CTX/xray"; cp "$ROOT/spofy/docker/Dockerfile" "$CTX/"
docker build -q --build-arg XRAY_REV="$REV" -t "$TAG" "$CTX" >/dev/null
docker run --rm --entrypoint xray "$TAG" version | head -1
echo "$TAG"
