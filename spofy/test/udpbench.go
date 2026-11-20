//go:build ignore

// udpbench: UDP echo origin and load generator for the Hysteria UDP e2e (spofy/test/e2e-hysteria-udp.sh).
//
//	udpbench echo  <listen>
//	udpbench burst <target> <senders> <interval-ms> <size> <seconds>
//	    every interval each sender sends one packet at once (a burst of <senders> packets), like the
//	    reproduction in quic-go issue #6189; prints the round-trip percentiles of the echoed packets.
//	udpbench rate  <target> <senders> <pps> <size> <seconds>
//	    <pps> packets per second in total, spread over the senders; prints sent/received and RTT.
package main

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

func atoi(s string) int { n, err := strconv.Atoi(s); must(err); return n }

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func echo(listen string) {
	addr, err := net.ResolveUDPAddr("udp", listen)
	must(err)
	c, err := net.ListenUDP("udp", addr)
	must(err)
	_ = c.SetReadBuffer(8 << 20)
	_ = c.SetWriteBuffer(8 << 20)
	b := make([]byte, 65536)
	for {
		n, from, err := c.ReadFromUDPAddrPort(b)
		if err != nil {
			continue
		}
		_, _ = c.WriteToUDPAddrPort(b[:n], from)
	}
}

type result struct {
	mu   sync.Mutex
	rtts []time.Duration
	sent atomic.Int64
}

func (r *result) add(d time.Duration) { r.mu.Lock(); r.rtts = append(r.rtts, d); r.mu.Unlock() }

func dialAll(target string, n int, res *result, base time.Time) []*net.UDPConn {
	addr, err := net.ResolveUDPAddr("udp", target)
	must(err)
	conns := make([]*net.UDPConn, n)
	for i := range conns {
		c, err := net.DialUDP("udp", nil, addr)
		must(err)
		_ = c.SetReadBuffer(4 << 20)
		conns[i] = c
		go func() {
			b := make([]byte, 65536)
			for {
				n, err := c.Read(b)
				if err != nil {
					return
				}
				if n >= 8 {
					sentAt := time.Duration(binary.BigEndian.Uint64(b))
					res.add(time.Since(base) - sentAt)
				}
			}
		}()
	}
	return conns
}

func report(mode string, res *result) {
	time.Sleep(1500 * time.Millisecond) // late echoes
	res.mu.Lock()
	r := append([]time.Duration(nil), res.rtts...)
	res.mu.Unlock()
	sort.Slice(r, func(i, j int) bool { return r[i] < r[j] })
	p := func(q float64) float64 {
		if len(r) == 0 {
			return -1
		}
		i := int(q * float64(len(r)))
		if i >= len(r) {
			i = len(r) - 1
		}
		return float64(r[i].Microseconds()) / 1000
	}
	sent := res.sent.Load()
	loss := 0.0
	if sent > 0 {
		loss = 100 * float64(sent-int64(len(r))) / float64(sent)
	}
	fmt.Printf("%s sent=%d recv=%d loss=%.2f%% p50=%.1f p90=%.1f p99=%.1f p999=%.1f max=%.1f\n",
		mode, sent, len(r), loss, p(0.5), p(0.9), p(0.99), p(0.999), p(1))
}

func packet(size int, base time.Time) []byte {
	b := make([]byte, size)
	binary.BigEndian.PutUint64(b, uint64(time.Since(base)))
	return b
}

func burst(target string, senders, intervalMs, size, seconds int) {
	res := &result{}
	base := time.Now()
	conns := dialAll(target, senders, res, base)
	t := time.NewTicker(time.Duration(intervalMs) * time.Millisecond)
	end := time.After(time.Duration(seconds) * time.Second)
loop:
	for {
		select {
		case <-t.C:
			for _, c := range conns {
				if _, err := c.Write(packet(size, base)); err == nil {
					res.sent.Add(1)
				}
			}
		case <-end:
			break loop
		}
	}
	report("burst", res)
}

func rate(target string, senders, pps, size, seconds int) {
	res := &result{}
	base := time.Now()
	conns := dialAll(target, senders, res, base)
	const tick = time.Millisecond
	t := time.NewTicker(tick)
	end := time.After(time.Duration(seconds) * time.Second)
	start := time.Now()
	next := 0
loop:
	for {
		select {
		case now := <-t.C:
			due := int64(float64(pps) * now.Sub(start).Seconds())
			for res.sent.Load() < due {
				if _, err := conns[next].Write(packet(size, base)); err != nil {
					break
				}
				res.sent.Add(1)
				next = (next + 1) % len(conns)
			}
		case <-end:
			break loop
		}
	}
	report("rate", res)
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: udpbench echo|burst|rate ...")
		os.Exit(2)
	}
	a := os.Args[2:]
	switch os.Args[1] {
	case "echo":
		echo(a[0])
	case "burst":
		burst(a[0], atoi(a[1]), atoi(a[2]), atoi(a[3]), atoi(a[4]))
	case "rate":
		rate(a[0], atoi(a[1]), atoi(a[2]), atoi(a[3]), atoi(a[4]))
	}
}
