#!/bin/bash
# Spofy Xray core on a remnanode node — run ON the node, one line:
#
#   sudo bash -c "$(curl -fsSL https://raw.githubusercontent.com/SpofyJet/xray-core/refs/heads/spofy/main/spofy/install.sh)"
#
# Other actions go after "_":   sudo bash -c "$(curl -fsSL …/install.sh)" _ plan|check|rollback
#   apply    (default) download the core from the GitHub release, verify sha256, build the image locally,
#            run the node's live config through it, pin it in docker-compose, recreate the container
#   plan     everything apply does up to (and including) the live-config test; changes nothing on the node
#   check    what is running now
#   rollback back to the image that ran before the Spofy core (exact digest) and the compose backup
#
# Needs a standard remnanode install (docker compose, container "remnanode") connected to the panel.
# apply drops live sessions for a few seconds. Env: SPOFY_RELEASE=<tag> (default latest), CONTAINER=remnanode,
# SKIP_PREFLIGHT=1 (only for a node with no inbounds yet: there is no live config to test).
set -euo pipefail
ACTION=${1:-apply}; C=${CONTAINER:-remnanode}
REPO=SpofyJet/xray-core; REL=${SPOFY_RELEASE:-latest}
BASE=remnawave/node:3.4.6@sha256:3d5fde656a36920f53c5c080f2dc821df8318f453803b07c4e5242752a549d69
die() { echo "ABORT: $*" >&2; exit 1; }
say() { echo "== $*"; }

[ "$(id -u)" = 0 ] || die "run as root (sudo)"
command -v docker >/dev/null || die "docker not found"
docker compose version >/dev/null 2>&1 || die "docker compose plugin not found"
docker inspect "$C" >/dev/null 2>&1 || die "container '$C' not found: install remnanode from the panel first"
lbl() { docker inspect "$C" --format "{{ index .Config.Labels \"com.docker.compose.$1\" }}"; }
DIR=$(lbl project.working_dir); SVC=$(lbl service); CF=$(lbl project.config_files); CF=${CF%%,*}
[ -n "$DIR" ] && [ -n "$SVC" ] && [ -f "$CF" ] || die "'$C' is not managed by docker compose (dir='$DIR' file='$CF')"
cd "$DIR"

state() {
  echo "compose: $CF (service $SVC)"
  echo "image:   $(docker inspect "$C" --format '{{.Config.Image}}')"
  echo "core:    $(docker exec "$C" xray version 2>/dev/null | sed -n 1p || echo '?')"
  echo "started: $(docker inspect "$C" --format '{{.State.Status}} {{.State.StartedAt}}')"
  grep -nE '^\s*image:' "$CF" || true
}

compose_up() { docker compose -f "$CF" up -d --no-deps "$SVC"; }

rollback() {
  [ -f .spofy-last-backup ] && [ -f .spofy-prev-image ] || die "no Spofy backup in $DIR: nothing to roll back"
  B=$(cat .spofy-last-backup); P=$(cat .spofy-prev-image); [ -f "$B" ] || die "backup $B is missing"
  say "restoring $B, image $P"
  cp -a "$CF" "$CF.spofy-rolledback-$(date -u +%Y%m%d-%H%M%S)"; cp -a "$B" "$CF"
  # pin the exact previous image (the backup may say :latest, which can point to a newer release by now)
  sed -i -E "s#^(\s*image:\s*)(remnawave/node|spofy/remnanode)[:@][^[:space:]]*#\1$P#" "$CF"
  compose_up; sleep 8; state
}

# Run the config the node's core is running right now through the new core, in a throwaway container with
# no network and the node's mounts read-only (certs). Nothing on the node changes.
preflight() {
  local t a rc; t=$(mktemp -d); chmod 700 "$t"
  a=$(ps -eo args | grep -m1 '[r]w-core -config @' | sed -E 's/.*-config @([^:]+):([^ ]+).*/\1 \2/' || true)
  set -- $a
  if [ -z "${2:-}" ]; then rm -rf "$t"; echo "no running core config found (node has no inbounds yet?)"; return 3; fi
  curl -sf --abstract-unix-socket "$1" "http://localhost$2" > "$t/c.json" || true
  if [ ! -s "$t/c.json" ]; then rm -rf "$t"; echo "could not read the live config"; return 3; fi
  local m; m=$(docker inspect "$C" --format '{{range .Mounts}}-v {{.Source}}:{{.Destination}}:ro {{end}}')
  if docker run --rm --network none $m -v "$t/c.json:/c.json:ro" --entrypoint xray "$TAG" run -test -c /c.json > "$t/out" 2>&1
  then rc=0; else rc=$?; fi
  grep -vE '^Xray |^A unified|Reading config' "$t/out" | tail -3; rm -rf "$t"; return $rc
}

prepare() {
  [ "$(uname -m)" = x86_64 ] || die "only x86_64 nodes are supported (this is $(uname -m))"
  command -v curl >/dev/null || die "curl not found"
  W=$(mktemp -d); trap 'rm -rf "$W"' EXIT
  if [ -n "${SPOFY_URL:-}" ]; then U=$SPOFY_URL
  elif [ "$REL" = latest ]; then U=https://github.com/$REPO/releases/latest/download
  else U=https://github.com/$REPO/releases/download/$REL; fi
  say "downloading the core ($REL)"
  curl -fsSL --retry 3 -o "$W/xray" "$U/xray-linux-amd64" || die "download failed: $U/xray-linux-amd64"
  curl -fsSL --retry 3 -o "$W/xray.sha256" "$U/xray-linux-amd64.sha256" || die "download failed: sha256"
  [ "$(awk '{print $1}' "$W/xray.sha256")" = "$(sha256sum "$W/xray" | awk '{print $1}')" ] || die "sha256 mismatch"
  chmod 0755 "$W/xray"; VER=$("$W/xray" version | sed -n 1p)
  REV=$(echo "$VER" | grep -o 'spofy-[0-9a-f]*' || true); XV=$(echo "$VER" | awk '{print $2}')
  [ -n "$REV" ] || die "downloaded binary is not a Spofy build: $VER"
  echo "$VER"
  TAG=spofy/remnanode:3.4.6-x$XV-$REV
  if docker image inspect "$TAG" >/dev/null 2>&1; then say "image $TAG already present"
  else
    say "building $TAG (base $BASE)"
    cat > "$W/Dockerfile" <<EOF
FROM $BASE
COPY xray /usr/local/bin/xray
RUN chmod 0755 /usr/local/bin/xray && /usr/local/bin/xray version | sed -n 1p | grep -q "$REV" && ls -l /usr/local/bin/rw-core
LABEL org.opencontainers.image.title="spofy-remnanode" org.opencontainers.image.base.name="remnawave/node:3.4.6" org.opencontainers.image.revision="$REV"
EOF
    docker build -q -t "$TAG" "$W" >/dev/null || die "image build failed"
  fi
  say "preflight: the node's live config through the new core"
  if preflight; then :
  elif [ "${SKIP_PREFLIGHT:-0}" = 1 ]; then echo "WARN: preflight skipped (SKIP_PREFLIGHT=1)"
  else die "the node's current config fails on $TAG (or cannot be read); nothing was changed"; fi
}

case "$ACTION" in
check) state ;;
rollback) rollback ;;
plan) state; prepare; say "plan OK: apply would pin $TAG in $CF and recreate '$SVC'" ;;
apply)
  say "state before"; state
  prepare
  if [ "$(docker inspect "$C" --format '{{.Config.Image}}')" = "$TAG" ] && docker exec "$C" xray version 2>/dev/null | sed -n 1p | grep -q "$REV"; then
    say "already running $TAG: nothing to do"; exit 0; fi
  n=$(grep -cE '^\s*image:\s*(remnawave/node|spofy/remnanode)[:@]' "$CF" || true)
  [ "$n" = 1 ] || die "expected exactly one remnanode image line in $CF, found $n"
  id=$(docker inspect "$C" --format '{{.Image}}')
  PREV=$(docker image inspect "$id" --format '{{if .RepoDigests}}{{index .RepoDigests 0}}{{end}}')
  [ -n "$PREV" ] || PREV=$(docker inspect "$C" --format '{{.Config.Image}}')
  TS=$(date -u +%Y%m%d-%H%M%S)
  cp -a "$CF" "$CF.bak-spofy-$TS"; echo "$CF.bak-spofy-$TS" > .spofy-last-backup
  [ -f .spofy-prev-image ] || echo "$PREV" > .spofy-prev-image   # keeps the original (pre-Spofy) image
  sed -i -E "s#^(\s*image:\s*)(remnawave/node|spofy/remnanode)[:@][^[:space:]]*#\1$TAG#" "$CF"
  docker compose -f "$CF" config -q || { cp -a "$CF.bak-spofy-$TS" "$CF"; die "compose file invalid after edit; restored"; }
  say "recreating $SVC"; compose_up
  for i in $(seq 1 30); do docker logs --since 2m "$C" 2>&1 | grep -a 'is up and running' >/dev/null && break; sleep 2; done
  if [ "$(docker inspect "$C" --format '{{.State.Running}}')" != true ] || ! docker exec "$C" xray version | sed -n 1p | grep -q "$REV"; then
    echo "core did not come up on $TAG: rolling back automatically" >&2; rollback; exit 2; fi
  say "state after"; state
  docker logs --since 2m "$C" 2>&1 | grep -a 'is up and running' >/dev/null && echo "OK: Spofy core $REV running" \
    || echo "OK: Spofy core $REV in the container (no 'is up and running' line yet: node without inbounds?)"
  echo "rollback: sudo bash -c \"\$(curl -fsSL https://raw.githubusercontent.com/$REPO/refs/heads/spofy/main/spofy/install.sh)\" _ rollback"
  ;;
*) die "unknown action '$ACTION' (apply|plan|check|rollback)" ;;
esac
