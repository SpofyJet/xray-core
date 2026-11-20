package dns_test

import (
	stdnet "net"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/xtls/xray-core/app/dispatcher"
	. "github.com/xtls/xray-core/app/dns"
	"github.com/xtls/xray-core/app/policy"
	"github.com/xtls/xray-core/app/proxyman"
	_ "github.com/xtls/xray-core/app/proxyman/outbound"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	feature_dns "github.com/xtls/xray-core/features/dns"
	"github.com/xtls/xray-core/proxy/freedom"
)

type lossyHandler struct {
	mu   sync.Mutex
	seen map[string]int
	drop map[string]int
}

func (h *lossyHandler) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	name := r.Question[0].Name
	h.mu.Lock()
	h.seen[name]++
	n := h.seen[name]
	h.mu.Unlock()
	if n <= h.drop[name] {
		return
	}
	answer := new(dns.Msg)
	answer.SetReply(r)
	rr, _ := dns.NewRR(name + " 60 IN A 192.0.2.7")
	answer.Answer = append(answer.Answer, rr)
	w.WriteMsg(answer)
}

func (h *lossyHandler) count(name string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.seen[name]
}

func lossyClient(t *testing.T, drop map[string]int) (*lossyHandler, feature_dns.Client) {
	t.Helper()
	conn, err := stdnet.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h := &lossyHandler{seen: map[string]int{}, drop: drop}
	started := make(chan struct{})
	server := &dns.Server{PacketConn: conn, Handler: h, NotifyStartedFunc: func() { close(started) }}
	go server.ActivateAndServe()
	<-started
	t.Cleanup(func() { server.Shutdown() })

	config := &core.Config{
		App: []*serial.TypedMessage{
			serial.ToTypedMessage(&Config{
				NameServer: []*NameServer{{
					Address: &net.Endpoint{
						Network: net.Network_UDP,
						Address: &net.IPOrDomain{Address: &net.IPOrDomain_Ip{Ip: []byte{127, 0, 0, 1}}},
						Port:    uint32(conn.LocalAddr().(*stdnet.UDPAddr).Port),
					},
				}},
			}),
			serial.ToTypedMessage(&dispatcher.Config{}),
			serial.ToTypedMessage(&proxyman.OutboundConfig{}),
			serial.ToTypedMessage(&policy.Config{}),
		},
		Outbound: []*core.OutboundHandlerConfig{{
			ProxySettings: serial.ToTypedMessage(&freedom.Config{
				FinalRules: []*freedom.FinalRuleConfig{{Action: freedom.RuleAction_Allow}},
			}),
		}},
	}
	v, err := core.New(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { v.Close() })
	return h, v.GetFeature(feature_dns.ClientType()).(feature_dns.Client)
}

func TestALostUDPQueryIsSentAgain(t *testing.T) {
	h, client := lossyClient(t, map[string]int{"lost.example.com.": 1})
	began := time.Now()
	ips, _, err := client.LookupIP("lost.example.com", feature_dns.IPOption{IPv4Enable: true})
	if err != nil {
		t.Fatal("the lost query was not sent again: ", err)
	}
	if len(ips) != 1 || !ips[0].Equal(net.IP{192, 0, 2, 7}) {
		t.Fatal("unexpected answer: ", ips)
	}
	if n := h.count("lost.example.com."); n != 2 {
		t.Fatalf("the server saw %d queries, want 2", n)
	}
	t.Logf("answered after %v", time.Since(began))
}

func TestAnAnsweredUDPQueryIsNotSentAgain(t *testing.T) {
	h, client := lossyClient(t, nil)
	if _, _, err := client.LookupIP("quick.example.com", feature_dns.IPOption{IPv4Enable: true}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	if n := h.count("quick.example.com."); n != 1 {
		t.Fatalf("the server saw %d queries, want 1", n)
	}
}
