#!/bin/bash
# REALITY + uTLS fingerprint compatibility. Runs inside the e2e container (see run-e2e.sh).
# SERVERS: server binaries; CLIENT: client binary (default xray-client = old core with the same uTLS as user apps);
# REALITY_EXTRA: extra JSON appended to server realitySettings, e.g. ',"requireMlkem":true'; FPS: fingerprints.
set -u
cd /b
[ -x /tmp/origin ] || { go build -o /tmp/origin origin.go; }
pgrep -f /tmp/origin >/dev/null || (/tmp/origin &)
# local fake "target" site for REALITY: TLS1.3 h2 server with self-signed cert (REALITY only needs TLS1.3 target)
cat > /tmp/target.go <<'G'
package main
import ("crypto/tls";"net/http";"log")
func main(){ c,err:=tls.LoadX509KeyPair("/tmp/tc.pem","/tmp/tk.pem"); if err!=nil{log.Fatal(err)}
 s:=&http.Server{Addr:"127.0.0.1:9443",TLSConfig:&tls.Config{Certificates:[]tls.Certificate{c},MinVersion:tls.VersionTLS13},Handler:http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){w.Write([]byte("target"))})}
 log.Fatal(s.ListenAndServeTLS("",""))}
G
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 2 -subj /CN=www.example.com -addext subjectAltName=DNS:www.example.com -keyout /tmp/tk.pem -out /tmp/tc.pem 2>/dev/null
pgrep -f /tmp/target >/dev/null || { go build -o /tmp/target /tmp/target.go && (/tmp/target &) ; }
sleep 1
CLIENT=${CLIENT:-xray-client}
KP=$(./$CLIENT x25519); PRIV=$(echo "$KP" | awk -F': ' '/Private/{print $2}'); PUB=$(echo "$KP" | awk -F': ' '/Public|Password/{print $2}' | head -1)
UUID=$(./$CLIENT uuid)
for SRV in $SERVERS; do
 for FP in ${FPS:-edge chrome firefox safari ios qq}; do
  cat > /tmp/rs.json <<J
{"log":{"loglevel":"warning"},"inbounds":[{"listen":"127.0.0.1","port":7443,"protocol":"vless","settings":{"clients":[{"id":"$UUID","flow":"xtls-rprx-vision"}],"decryption":"none"},
 "streamSettings":{"network":"tcp","security":"reality","realitySettings":{"target":"127.0.0.1:9443","serverNames":["www.example.com"],"privateKey":"$PRIV","shortIds":["ab12"]${REALITY_EXTRA:-}}}}],
 "outbounds":[{"protocol":"freedom","settings":{"finalRules":[{"action":"allow","ip":["127.0.0.0/8"]}]}}]}
J
  cat > /tmp/rc.json <<J
{"log":{"loglevel":"warning"},"inbounds":[{"listen":"127.0.0.1","port":1081,"protocol":"socks"}],
 "outbounds":[{"protocol":"vless","settings":{"vnext":[{"address":"127.0.0.1","port":7443,"users":[{"id":"$UUID","flow":"xtls-rprx-vision","encryption":"none"}]}]},
 "streamSettings":{"network":"tcp","security":"reality","realitySettings":{"serverName":"www.example.com","fingerprint":"$FP","publicKey":"$PUB","shortId":"ab12"}}}]}
J
  ./$SRV run -c /tmp/rs.json >/tmp/rs.log 2>&1 & sp=$!
  ./$CLIENT run -c /tmp/rc.json >/tmp/rc.log 2>&1 & cp=$!
  sleep 1.2
  r=$(curl -s -m 6 --socks5-hostname 127.0.0.1:1081 http://127.0.0.1:18080/s); [ "$r" = ok ] && res=OK || res=FAIL
  sp2=""; [ $res = OK ] && sp2=$(curl -s -o /dev/null -m 10 -w '%{speed_download}' --socks5-hostname 127.0.0.1:1081 "http://127.0.0.1:18080/f?mb=300" | awk '{printf "%.0f Mbit/s", $1*8/1e6}')
  kill $sp $cp 2>/dev/null; wait $sp $cp 2>/dev/null
  printf "server=%-14s fp=%-8s %-5s %s\n" "$SRV" "$FP" "$res" "$sp2"; [ $res = OK ] || FAILS=$((${FAILS:-0}+1))
 done
done

echo "FAILS=${FAILS:-0}"
