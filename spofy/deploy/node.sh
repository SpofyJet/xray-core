#!/bin/bash
# Deploy / roll back the Spofy remnanode image on one node over SSH.
#
#   spofy/deploy/node.sh <ssh-host> plan     [<image-tag>]   # read-only: what would change
#   spofy/deploy/node.sh <ssh-host> apply    [<image-tag>]   # ship image, test live config, pin compose, recreate
#   spofy/deploy/node.sh <ssh-host> preflight [<image-tag>]  # read-only: run the node's live config through the new core
#   spofy/deploy/node.sh <ssh-host> check                    # versions, listeners, last rw-node lines
#   spofy/deploy/node.sh <ssh-host> rollback                 # restore compose + previous image digest
#
# apply recreates the remnanode container: every live session on the node drops for a few seconds.
# The image is shipped with docker save | ssh docker load (no registry needed).
# <image-tag> defaults to the newest local spofy/remnanode image.
set -euo pipefail
HOST=${1:?ssh host}; ACTION=${2:?plan|apply|check|rollback}; TAG=${3:-}
CONTAINER=${CONTAINER:-remnanode}
R() { ssh -o BatchMode=yes -o ConnectTimeout=10 "$HOST" "$@"; }
if [ -z "$TAG" ] && [ "$ACTION" != rollback ] && [ "$ACTION" != check ]; then
  TAG=$(docker images spofy/remnanode --format '{{.Repository}}:{{.Tag}}' | head -1)
  [ -n "$TAG" ] || { echo "no local spofy/remnanode image: run spofy/build.sh && spofy/docker/build-image.sh"; exit 1; }
  echo "image: $TAG (newest local)"
fi

# Run the config the node's core is running right now through the new core, in a throwaway container with no
# network (its own netns, so the API abstract socket does not clash) and the same mounts read-only (certs).
# Nothing on the node changes.
preflight() {
  R "set -e; t=\$(mktemp -d); trap 'rm -rf \$t' EXIT; chmod 700 \$t
     a=\$(ps -eo args | grep -m1 '[r]w-core -config @' | sed -E 's/.*-config @([^:]+):([^ ]+).*/\\1 \\2/'); set -- \$a
     [ -n \"\${2:-}\" ] || { echo 'preflight: cannot find the running core config'; exit 3; }
     curl -sf --abstract-unix-socket \"\$1\" \"http://localhost\$2\" > \$t/c.json
     [ -s \$t/c.json ] || { echo 'preflight: empty config'; exit 3; }
     m=\$(docker inspect $CONTAINER --format '{{range .Mounts}}-v {{.Source}}:{{.Destination}}:ro {{end}}')
     if docker run --rm --network none \$m -v \$t/c.json:/c.json:ro --entrypoint xray '$TAG' run -test -c /c.json > \$t/out 2>&1
     then rc=0; else rc=\$?; fi
     grep -vE '^Xray |^A unified|Reading config' \$t/out | tail -3; exit \$rc"
}

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
preflight)
  R "docker image inspect '$TAG' >/dev/null 2>&1" || { echo "== shipping $TAG"; docker save "$TAG" | gzip -1 | R "gunzip | docker load -q"; }
  preflight
  ;;
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
  echo "== preflight: live config through the new core"
  preflight || { echo "ABORT: the node's current config fails on $TAG; nothing was changed on the node"; exit 1; }
  R "set -e; cd '$D'; cp -a docker-compose.yml docker-compose.yml.bak-spofy-$TS
     [ -f .spofy-prev-image ] || echo '$PREV' > .spofy-prev-image
     echo 'docker-compose.yml.bak-spofy-$TS' > .spofy-last-backup
     n=\$(grep -cE '^\s*image:\s*(remnawave/node|spofy/remnanode)[:@]' docker-compose.yml || true)
     [ \"\$n\" = 1 ] || { echo \"ABORT: expected exactly one remnanode image line in docker-compose.yml, found \$n\"; exit 4; }
     sed -i -E 's#^(\s*image:\s*)(remnawave/node|spofy/remnanode)[:@][^[:space:]]*#\1$TAG#' docker-compose.yml; grep -nE '^\s*image:' docker-compose.yml
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
     sed -i -E \"s#^(\\s*image:\\s*)(remnawave/node|spofy/remnanode)[:@][^[:space:]]*#\\1\$P#\" docker-compose.yml; grep -nE '^\\s*image:' docker-compose.yml
     docker compose up -d --no-deps $CONTAINER"
  sleep 8; remote_state
  ;;
*) echo "unknown action $ACTION"; exit 1;;
esac
