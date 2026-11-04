#!/bin/bash
# runs inside the bench container (own netns). usage: run.sh
set -u
cd /b
apt-get -qq update >/dev/null 2>&1 && apt-get -qq install -y iproute2 >/dev/null 2>&1 || echo "WARN: iproute2 install failed"
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 2 -subj /CN=bench.local -addext subjectAltName=DNS:bench.local -keyout /tmp/k.pem -out /tmp/c.pem 2>/dev/null
PIN=$(openssl x509 -in /tmp/c.pem -outform DER | sha256sum | cut -d' ' -f1)
go build -o /tmp/origin origin.go && (/tmp/origin &) ; sleep 2
QP='"quicParams":{"congestion":"bbr","keepAlivePeriod":5,"maxIncomingStreams":1024,"maxStreamReceiveWindow":8388608,"initStreamReceiveWindow":8388608,"maxConnectionReceiveWindow":20971520,"initConnectionReceiveWindow":20971520}'
cat > /tmp/srv.json <<J
{"log":{"loglevel":"warning"},
 "inbounds":[{"tag":"hy","listen":"127.0.0.1","port":8443,"protocol":"hysteria","settings":{"version":2,"clients":[{"auth":"benchpass","email":"u1"}]},
  "streamSettings":{"network":"hysteria","security":"tls","finalmask":{$QP},
   "tlsSettings":{"alpn":["h3"],"serverName":"bench.local","certificates":[{"certificateFile":"/tmp/c.pem","keyFile":"/tmp/k.pem"}]},
   "hysteriaSettings":{"version":2}}}],
 "outbounds":[{"protocol":"freedom","settings":{"finalRules":[{"action":"allow","ip":["127.0.0.0/8"]}]}}]}
J
cat > /tmp/cli.json <<J
{"log":{"loglevel":"warning"},
 "inbounds":[{"listen":"127.0.0.1","port":1080,"protocol":"socks","settings":{"udp":true}}],
 "outbounds":[{"protocol":"hysteria","settings":{"version":2,"address":"127.0.0.1","port":8443},
  "streamSettings":{"network":"hysteria","security":"tls","finalmask":{$QP},
   "tlsSettings":{"serverName":"bench.local","alpn":["h3"],"pinnedPeerCertSha256":"$PIN"},
   "hysteriaSettings":{"version":2,"auth":"benchpass"}}}]}
J
netem() { tc qdisc del dev lo root 2>/dev/null; [ -n "$1" ] && [ "$1" != none ] && tc qdisc add dev lo root netem $1; }
one() { # bin label netem
  local bin=$1 label=$2 ne=$3
  ./$bin run -c /tmp/srv.json > /tmp/srv.$label.log 2>&1 & local sp=$!
  ./$bin run -c /tmp/cli.json > /tmp/cli.$label.log 2>&1 & local cp=$!
  sleep 1.5
  netem "$ne"
  local ok=$(curl -s -m 10 --socks5-hostname 127.0.0.1:1080 http://127.0.0.1:18080/s)
  local sp_list=""
  for i in $(seq 1 ${RUNS:-3}); do
    s=$(curl -s -o /dev/null -m 15 -w '%{speed_download}' --socks5-hostname 127.0.0.1:1080 "http://127.0.0.1:18080/f?mb=${MB:-200}")
    sp_list="$sp_list $(awk -v s=$s 'BEGIN{printf "%.0f", s*8/1e6}')"
  done
  # time-to-first-byte of 20 small requests (new stream each) 
  local ttfb=$(for i in $(seq 1 20); do curl -s -o /dev/null -m 10 -w '%{time_starttransfer}\n' --socks5-hostname 127.0.0.1:1080 http://127.0.0.1:18080/s; done | sort -n | awk '{a[NR]=$1} END{printf "%.0f/%.0f", a[int(NR/2)]*1000, a[NR]*1000}')
  netem ""
  local rss=$(ps -o rss= -p $sp)
  kill $sp $cp 2>/dev/null; wait $sp $cp 2>/dev/null
  local errs=$(grep -ciE "panic|error" /tmp/srv.$label.log /tmp/cli.$label.log | awk -F: '{s+=$2} END{print s}')
  printf "%-6s %-34s ok=%-3s Mbit/s:%-18s ttfb_ms(med/max)=%-9s srvRSS=%sMB errs=%s\n" "$label" "${ne:-none}" "$ok" "$sp_list" "$ttfb" "$((rss/1024))" "$errs"
}
IFS="|" read -ra SC <<< "${SCEN:-|delay 25ms|delay 25ms loss 1%|delay 25ms loss 3%|delay 25ms 10ms distribution normal loss 1%}"
for ne in "${SC[@]}"; do
  for b in ${BINS:-prod stock new}; do one xray-$b $b "$ne"; done
done
tail -5 /tmp/srv.new.log
