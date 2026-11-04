#!/bin/bash
# CPU time (server process, utime+stime) per GB through hysteria, no netem. Alternating order, 3 rounds.
cd /b  # usage: spofy/test/run-e2e.sh <dir> cpu BINS="stock spofy" MB=1024 (client = xray-prod)
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 2 -subj /CN=bench.local -addext subjectAltName=DNS:bench.local -keyout /tmp/k.pem -out /tmp/c.pem 2>/dev/null
PIN=$(openssl x509 -in /tmp/c.pem -outform DER | sha256sum | cut -d' ' -f1)
go build -o /tmp/origin origin.go && (/tmp/origin &); sleep 2
QP='"quicParams":{"congestion":"bbr","keepAlivePeriod":5,"maxIncomingStreams":1024,"maxStreamReceiveWindow":8388608,"initStreamReceiveWindow":8388608,"maxConnectionReceiveWindow":20971520,"initConnectionReceiveWindow":20971520}'
cat > /tmp/srv.json <<J
{"log":{"loglevel":"none"},"inbounds":[{"listen":"127.0.0.1","port":8443,"protocol":"hysteria","settings":{"version":2,"clients":[{"auth":"benchpass","email":"u1"}]},
 "streamSettings":{"network":"hysteria","security":"tls","finalmask":{$QP},"tlsSettings":{"alpn":["h3"],"serverName":"bench.local","certificates":[{"certificateFile":"/tmp/c.pem","keyFile":"/tmp/k.pem"}]},"hysteriaSettings":{"version":2}}}],
 "outbounds":[{"protocol":"freedom","settings":{"finalRules":[{"action":"allow","ip":["127.0.0.0/8"]}]}}]}
J
cat > /tmp/cli.json <<J
{"log":{"loglevel":"none"},"inbounds":[{"listen":"127.0.0.1","port":1080,"protocol":"socks"}],
 "outbounds":[{"protocol":"hysteria","settings":{"version":2,"address":"127.0.0.1","port":8443},
  "streamSettings":{"network":"hysteria","security":"tls","finalmask":{$QP},"tlsSettings":{"serverName":"bench.local","alpn":["h3"],"pinnedPeerCertSha256":"$PIN"},"hysteriaSettings":{"version":2,"auth":"benchpass"}}}]}
J
TCK=$(getconf CLK_TCK)
cpu() { awk -v t=$TCK '{printf "%.2f", ($14+$15)/t}' /proc/$1/stat; }
for r in 1 2 3; do for b in $BINS; do
  ./xray-$b run -c /tmp/srv.json >/dev/null 2>&1 & sp=$!
  ./xray-prod run -c /tmp/cli.json >/dev/null 2>&1 & cp=$!   # same client for all
  sleep 1.5; curl -s -o /dev/null --socks5-hostname 127.0.0.1:1080 "http://127.0.0.1:18080/f?mb=16"
  c0=$(cpu $sp); s=$(curl -s -o /dev/null -w '%{speed_download}' --socks5-hostname 127.0.0.1:1080 "http://127.0.0.1:18080/f?mb=${MB:-1024}"); c1=$(cpu $sp)
  awk -v b=$b -v r=$r -v c0=$c0 -v c1=$c1 -v s=$s -v mb=${MB:-1024} 'BEGIN{printf "%-6s round=%d server_cpu_s_per_GB=%.2f  Mbit/s=%.0f\n", b, r, (c1-c0)*1024/mb, s*8/1e6}'
  kill $sp $cp; wait $sp $cp 2>/dev/null
done; done
