#!/bin/bash
# Spofy e2e harness (isolated container, own netns; netem only on the container's loopback).
# usage: spofy/test/run-e2e.sh <workdir> reality|hysteria|cpu [VAR=value ...]
#   <workdir> must contain the binaries under test named xray-<label> (and xray-client for reality).
# examples:
#   spofy/test/run-e2e.sh /tmp/w reality SERVERS="xray-spofy" REALITY_EXTRA=',"requireMlkem":true'
#   spofy/test/run-e2e.sh /tmp/w hysteria BINS="stock spofy" SCEN="none|delay 25ms loss 1%" MB=150 RUNS=3
set -euo pipefail
W=$(readlink -f "$1"); T=$2; shift 2
HERE=$(cd "$(dirname "$0")" && pwd)
cp "$HERE/origin.go" "$HERE"/e2e-*.sh "$W/"
ENVS=(); for kv in "$@"; do ENVS+=(-e "$kv"); done
CACHE=${SPOFY_GO_CACHE:-/var/cache/spofy-xray-build}
exec docker run --rm --cap-add NET_ADMIN --memory=2g --cpus=2 "${ENVS[@]}" \
  -v "$W:/b" -v "$CACHE/gomod:/go/pkg/mod" -v "$CACHE/gocache:/root/.cache" \
  golang:1.27 bash "/b/e2e-$T.sh"
