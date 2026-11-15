#!/bin/bash
# Hysteria UDP e2e, runs inside the bench container (own netns; netem on its loopback):
#   udpbench -> client (dokodemo-door UDP) -> Hysteria -> server -> freedom -> udpbench echo
# Server binaries SRVS are compared with the same client CLI, ROUNDS times interleaved.
#   BURST="<senders> <interval-ms> <size> <seconds>"  bursts of small datagrams (quic-go #6189), netem NE
#   RATES="<senders> <pps> <size> <seconds>|..."      fixed packet rates without netem: server CPU per packet
# usage: spofy/test/run-e2e.sh <dir> hysteria-udp SRVS="prod new" CLI=new
set -u
cd /b
apt-get -qq update >/dev/null 2>&1 && apt-get -qq install -y iproute2 >/dev/null 2>&1 || echo "WARN: iproute2 install failed"
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 2 -subj /CN=bench.local -addext subjectAltName=DNS:bench.local -keyout /tmp/k.pem -out /tmp/c.pem 2>/dev/null
PIN=$(openssl x509 -in /tmp/c.pem -outform DER | sha256sum | cut -d' ' -f1)
go build -o /tmp/udpbench udpbench.go && (/tmp/udpbench echo 127.0.0.1:19000 &) ; sleep 1
QP='"quicParams":{"congestion":"bbr","keepAlivePeriod":5,"maxIncomingStreams":1024,"maxStreamReceiveWindow":8388608,"initStreamReceiveWindow":8388608,"maxConnectionReceiveWindow":20971520,"initConnectionReceiveWindow":20971520}'
cat > /tmp/srv.json <<J
{"log":{"loglevel":"warning"},
 "inbounds":[{"tag":"hy","listen":"127.0.0.1","port":8443,"protocol":"hysteria","settings":{"version":2,"clients":[{"auth":"benchpass","email":"u1"}]},
  "streamSettings":{"network":"hysteria","security":"tls","finalmask":{$QP},
   "tlsSettings":{"alpn":["h3"],"serverName":"bench.local","certificates":[{"certificateFile":"/tmp/c.pem","keyFile":"/tmp/k.pem"}]},
   "hysteriaSettings":{"version":2}}}],
 "outbounds":[{"protocol":"freedom","settings":{"finalRules":[{"action":"allow","ip":["127.0.0.0/8"]}]}}],
 "stats":{},"policy":{"levels":{"0":{"statsUserUplink":true,"statsUserDownlink":true}},"system":{"statsInboundUplink":true,"statsInboundDownlink":true}}}
J
cat > /tmp/cli.json <<J
{"log":{"loglevel":"warning"},
 "inbounds":[{"listen":"127.0.0.1","port":1090,"protocol":"dokodemo-door","settings":{"address":"127.0.0.1","port":19000,"network":"udp"}}],
 "outbounds":[{"protocol":"hysteria","settings":{"version":2,"address":"127.0.0.1","port":8443},
  "streamSettings":{"network":"hysteria","security":"tls","finalmask":{$QP},
   "tlsSettings":{"serverName":"bench.local","alpn":["h3"],"pinnedPeerCertSha256":"$PIN"},
   "hysteriaSettings":{"version":2,"auth":"benchpass"}}}]}
J
netem() { tc qdisc del dev lo root 2>/dev/null; [ -n "$1" ] && [ "$1" != none ] && tc qdisc add dev lo root netem $1; }
ticks() { awk '{print $14+$15}' /proc/$1/stat; }
one() { # srv-label netem mode args...
  local srv=$1 ne=$2 mode=$3; shift 3
  ./xray-$srv run -c /tmp/srv.json > /tmp/srv.$srv.log 2>&1 & local sp=$!
  ./xray-${CLI:-new} run -c /tmp/cli.json > /tmp/cli.log 2>&1 & local cp=$!
  sleep 1.5
  /tmp/udpbench rate 127.0.0.1:1090 1 20 64 1 >/dev/null # connect
  netem "$ne"
  local c0=$(ticks $sp)
  local out=$(/tmp/udpbench $mode 127.0.0.1:1090 "$@")
  local c1=$(ticks $sp)
  netem ""
  local rss=$(ps -o rss= -p $sp)
  kill $sp $cp 2>/dev/null; wait $sp $cp 2>/dev/null
  local errs=$(grep -ciE "panic|fatal" /tmp/srv.$srv.log /tmp/cli.log | awk -F: '{s+=$2} END{print s}')
  printf "%-5s %-14s %-22s %s srv_cpu_ms=%d srvRSS=%sMB panics=%s\n" "$srv" "${ne:-none}" "$mode $*" "$out" "$(( (c1-c0)*10 ))" "$((rss/1024))" "$errs"
}
IFS="|" read -ra RT <<< "${RATES:-16 20000 64 10|16 20000 1200 10|32 400000 64 6}"
for r in $(seq 1 ${ROUNDS:-3}); do
  for s in ${SRVS:-prod new}; do one "$s" "${NE:-delay 10ms}" burst ${BURST:-64 40 64 20}; done
  for rate in "${RT[@]}"; do
    for s in ${SRVS:-prod new}; do one "$s" none rate $rate; done
  done
done
