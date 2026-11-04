#!/bin/bash
# Deploy / roll back the Spofy remnanode image on one node over SSH.
#
#   spofy/deploy/node.sh <ssh-host> plan     [<image-tag>]   # read-only: what would change
#   spofy/deploy/node.sh <ssh-host> apply    <image-tag>     # ship image, pin compose, recreate container
#   spofy/deploy/node.sh <ssh-host> check                    # versions, listeners, last rw-node lines
#   spofy/deploy/node.sh <ssh-host> rollback                 # restore compose + previous image digest
#
# apply recreates the remnanode container: every live session on the node drops for a few seconds.
# The image is shipped with docker save | ssh docker load (no registry needed).
set -euo pipefail
HOST=${1:?ssh host}; ACTION=${2:?plan|apply|check|rollback}; TAG=${3:-}
CONTAINER=${CONTAINER:-remnanode}
R() { ssh -o BatchMode=yes -o ConnectTimeout=10 "$HOST" "$@"; }

compose_dir() { R "docker inspect $CONTAINER --format '{{ index .Config.Labels \"com.docker.compose.project.working_dir\" }}'"; }
remote_state() {
  R "set -e; c=$CONTAINER
     echo image_ref=\$(docker inspect \$c --format '{{.Config.Image}}')
     id=\$(docker inspect \$c --format '{{.Image}}'); echo image_id=\$id
     echo image_digest=\$(docker image inspect \$id --format '{{if .RepoDigests}}{{index .RepoDigests 0}}{{end}}')
     echo xray=\"\$(docker exec \$c xray version | head -1)\"
     echo started=\$(docker inspect \$c --format '{{.State.StartedAt}}')
     echo listen_tcp_ports=\$(ss -Htlnp | awk '/rw-core/{p=\$4; sub(/.*:/,\"\",p); print p}' | sort -un | tr '\\n' ,) udp443=\$(ss -Hulnp 'sport = :443' | grep -c rw-core || true)"
}

case "$ACTION" in
plan|check)
  echo "== $HOST"; D=$(compose_dir); echo "compose_dir=$D"; remote_state
  R "grep -nE '^\s*image:' '$D/docker-compose.yml'; ls -1 '$D' | grep -E 'spofy' || true"
  if [ "$ACTION" = check ]; then R "docker logs --since 10m $CONTAINER 2>&1 | grep -aE 'XRay Core|error|panic' | tail -5"; fi
  [ -n "$TAG" ] && { echo "-- would ship: $TAG ($(docker image inspect "$TAG" --format '{{.Id}}'))"; docker run --rm --entrypoint xray "$TAG" version | head -1; }
  ;;
apply)
  [ -n "$TAG" ] || { echo "image tag required"; exit 1; }
  docker image inspect "$TAG" >/dev/null
  D=$(compose_dir); TS=$(date -u +%Y%m%d-%H%M%S)
  echo "== state before"; remote_state | tee /dev/stderr | grep -E '^image_digest=' | cut -d= -f2- > /tmp/.spofy-prev.$$ || true
  PREV=$(cat /tmp/.spofy-prev.$$); rm -f /tmp/.spofy-prev.$$
  [ -n "$PREV" ] || { echo "cannot resolve current image digest; refusing (rollback would be impossible)"; exit 1; }
  echo "== shipping $TAG"; docker save "$TAG" | gzip -1 | R "gunzip | docker load -q"
  R "set -e; cd '$D'; cp -a docker-compose.yml docker-compose.yml.bak-spofy-$TS
     [ -f .spofy-prev-image ] || echo '$PREV' > .spofy-prev-image
     echo 'docker-compose.yml.bak-spofy-$TS' > .spofy-last-backup
     sed -i -E 's#^(\s*image:\s*).*#\1$TAG#' docker-compose.yml; grep -nE '^\s*image:' docker-compose.yml
     docker compose up -d --no-deps $CONTAINER"
  echo "== waiting for XRay Core"; for i in $(seq 1 30); do
    if R "docker logs --since 2m $CONTAINER 2>&1 | grep -aq 'is up and running'"; then break; fi; sleep 2; done
  remote_state
  R "docker exec $CONTAINER xray version | head -1 | grep -q spofy-" && echo "OK: spofy core running" || { echo "WARN: spofy core not detected — consider: $0 $HOST rollback"; exit 2; }
  ;;
rollback)
  D=$(compose_dir)
  R "set -e; cd '$D'; B=\$(cat .spofy-last-backup); P=\$(cat .spofy-prev-image)
     echo restoring \$B, image \$P
     cp -a docker-compose.yml docker-compose.yml.spofy-rolledback-\$(date -u +%Y%m%d-%H%M%S)
     cp -a \"\$B\" docker-compose.yml
     # pin the exact previous image (the backup may say :latest, which can point to a newer release now)
     sed -i -E \"s#^(\\s*image:\\s*).*#\\1\$P#\" docker-compose.yml; grep -nE '^\\s*image:' docker-compose.yml
     docker compose up -d --no-deps $CONTAINER"
  sleep 8; remote_state
  ;;
*) echo "unknown action $ACTION"; exit 1;;
esac
