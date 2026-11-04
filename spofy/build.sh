#!/bin/bash
# Build the Spofy Xray binary exactly like upstream's release workflow (linux/amd64 by default),
# inside a resource-limited container so it does not starve services on this host.
# usage: spofy/build.sh [GOARCH]   -> spofy/dist/xray-linux-<arch> (+ .sha256)
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
ARCH=${1:-amd64}
GO_IMAGE=${GO_IMAGE:-golang:1.27.3}
CACHE=${SPOFY_GO_CACHE:-/var/cache/spofy-xray-build}
mkdir -p "$ROOT/spofy/dist" "$CACHE/gomod" "$CACHE/gocache"
if [ -n "$(git -C "$ROOT" status --porcelain --untracked-files=no)" ]; then DIRTY=-dirty; else DIRTY=; fi
REV="spofy-$(git -C "$ROOT" rev-parse --short=7 HEAD)$DIRTY"
OUT=spofy/dist/xray-linux-$ARCH
docker run --rm --memory=${BUILD_MEM:-2g} --cpus=${BUILD_CPUS:-2} \
  -e CGO_ENABLED=0 -e GOOS=linux -e GOARCH="$ARCH" -e GOFLAGS=-mod=mod -e GOTOOLCHAIN=local \
  -v "$ROOT:/src" -v "$CACHE/gomod:/go/pkg/mod" -v "$CACHE/gocache:/root/.cache" -w /src "$GO_IMAGE" \
  sh -ec "go version; nice go build -o $OUT -trimpath -buildvcs=false -gcflags='all=-l=4' \
    -ldflags='-X github.com/xtls/xray-core/core.build=$REV -s -w -buildid= -checklinkname=0' -v ./main >/dev/null"
sha256sum "$ROOT/$OUT" | sed "s#$ROOT/##" > "$ROOT/$OUT.sha256"
[ "$ARCH" = amd64 ] && "$ROOT/$OUT" version | head -1
cat "$ROOT/$OUT.sha256"
