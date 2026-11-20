package hysteria

import (
	"bytes"
	"encoding/binary"
	stderrors "errors"
	"io"
	"math/rand"
	"strings"
	"testing"

	"github.com/apernet/quic-go/quicvarint"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/transport/internet/stat"
)

// benchPacketConn stands in for hysteria.InterConn: Read copies one datagram, like InterConn.Read.
type benchPacketConn struct {
	datagram []byte
}

func (c *benchPacketConn) Read(p []byte) (int, error) { return copy(p, c.datagram), nil }

func (c *benchPacketConn) ReadPacket() ([]byte, error) { return c.datagram, nil }

func (c *benchPacketConn) Write(p []byte) (int, error) { return len(p), nil }

func serializedUDPMessage(tb testing.TB, addr string, size int) []byte {
	tb.Helper()
	m := &UDPMessage{FragCount: 1, Addr: addr, Data: make([]byte, size)}
	b := make([]byte, m.Size())
	if n := m.Serialize(b); n != len(b) {
		tb.Fatalf("serialize: %d != %d", n, len(b))
	}
	return b
}

func benchmarkSpofyUDPUpload(b *testing.B, size int) {
	conn := &benchPacketConn{datagram: serializedUDPMessage(b, "203.0.113.7:443", size)}
	r := newUDPReader(conn)
	b.ReportAllocs()
	b.SetBytes(int64(size))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mb, err := r.ReadMultiBuffer()
		if err != nil {
			b.Fatal(err)
		}
		buf.ReleaseMulti(mb)
	}
}

func BenchmarkSpofyUDPUpload64(b *testing.B)   { benchmarkSpofyUDPUpload(b, 64) }
func BenchmarkSpofyUDPUpload1200(b *testing.B) { benchmarkSpofyUDPUpload(b, 1200) }

func benchmarkSpofyUDPDownload(b *testing.B, size int) {
	w := &UDPWriter{writer: io.Writer(&benchPacketConn{}), addr: "203.0.113.7:443"}
	payload := make([]byte, size)
	b.ReportAllocs()
	b.SetBytes(int64(size))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		x := buf.New()
		x.Write(payload)
		// freedom allocates a fresh Destination for every packet it reads
		x.UDP = &net.Destination{Network: net.Network_UDP, Address: net.IPAddress([]byte{203, 0, 113, 7}), Port: 443}
		if err := w.WriteMultiBuffer(buf.MultiBuffer{x}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSpofyUDPDownload64(b *testing.B)   { benchmarkSpofyUDPDownload(b, 64) }
func BenchmarkSpofyUDPDownload1200(b *testing.B) { benchmarkSpofyUDPDownload(b, 1200) }

// parseUDPMessageOld is ParseUDPMessage before the Spofy change (bytes.Buffer + binary.Read), the reference
// for the hand-written parser.
func parseUDPMessageOld(msg []byte) (*UDPMessage, error) {
	m := &UDPMessage{}
	buf := bytes.NewBuffer(msg)
	if err := binary.Read(buf, binary.BigEndian, &m.SessionID); err != nil {
		return nil, err
	}
	if err := binary.Read(buf, binary.BigEndian, &m.PacketID); err != nil {
		return nil, err
	}
	if err := binary.Read(buf, binary.BigEndian, &m.FragID); err != nil {
		return nil, err
	}
	if err := binary.Read(buf, binary.BigEndian, &m.FragCount); err != nil {
		return nil, err
	}
	lAddr, err := quicvarint.Read(buf)
	if err != nil {
		return nil, err
	}
	if lAddr == 0 || lAddr > MaxMessageLength {
		return nil, stderrors.New("invalid address length")
	}
	bs := buf.Bytes()
	if len(bs) <= int(lAddr) {
		return nil, stderrors.New("invalid message length")
	}
	m.Addr = string(bs[:lAddr])
	m.Data = bs[lAddr:]
	return m, nil
}

func checkParseSame(t testing.TB, in []byte) {
	want, wantErr := parseUDPMessageOld(in)
	got, gotErr := ParseUDPMessage(in)
	if (wantErr == nil) != (gotErr == nil) {
		t.Fatalf("%x: old err %v, new err %v", in, wantErr, gotErr)
	}
	if wantErr != nil {
		return
	}
	if got.SessionID != want.SessionID || got.PacketID != want.PacketID || got.FragID != want.FragID ||
		got.FragCount != want.FragCount || got.Addr != want.Addr || !bytes.Equal(got.Data, want.Data) {
		t.Fatalf("%x: old %+v, new %+v", in, want, got)
	}
	// reusing the previous address must not change the result
	var m UDPMessage
	if err := parseUDPMessage(in, &m, want.Addr); err != nil || m.Addr != want.Addr || !bytes.Equal(m.Data, want.Data) {
		t.Fatalf("%x: reuse addr: %+v %v", in, m, err)
	}
	if err := parseUDPMessage(in, &m, want.Addr+"x"); err != nil || m.Addr != want.Addr {
		t.Fatalf("%x: other last addr: %+v %v", in, m, err)
	}
}

func TestSpofyParseUDPMessageMatchesOld(t *testing.T) {
	seeds := [][]byte{
		nil,
		{},
		{1, 2, 3},
		make([]byte, 8), make([]byte, 9),
		serializedUDPMessage(t, "203.0.113.7:443", 1),
		serializedUDPMessage(t, "[2001:db8::1]:53", 100),
		serializedUDPMessage(t, "example.com:3478", 1200),
		serializedUDPMessage(t, strings.Repeat("a", 64)+":1", 10), // 2-byte varint length
	}
	m := &UDPMessage{SessionID: 0xdeadbeef, PacketID: 0x1234, FragID: 2, FragCount: 3, Addr: "1.2.3.4:5", Data: []byte("xyz")}
	b := make([]byte, m.Size())
	m.Serialize(b)
	binary.BigEndian.PutUint32(b, m.SessionID) // Serialize leaves the session ID to InterConn.Write
	seeds = append(seeds, b, b[:len(b)-3], b[:len(b)-4], append([]byte{}, b[:9]...))
	tooLong := serializedUDPMessage(t, strings.Repeat("a", MaxMessageLength+1), 1)
	seeds = append(seeds, tooLong)
	for _, s := range seeds {
		checkParseSame(t, s)
	}
	rnd := rand.New(rand.NewSource(1))
	for i := 0; i < 200000; i++ {
		in := make([]byte, rnd.Intn(40))
		rnd.Read(in)
		if len(in) > 8 && rnd.Intn(2) == 0 {
			in[8] = byte(rnd.Intn(30)) // a short 1-byte address length, so that some inputs parse
		}
		checkParseSame(t, in)
	}
}

func FuzzSpofyParseUDPMessage(f *testing.F) {
	f.Add(serializedUDPMessage(f, "203.0.113.7:443", 5))
	f.Add([]byte{0, 0, 0, 0, 0, 0, 0, 1, 0x40, 1, 'a', 'b'})
	f.Fuzz(func(t *testing.T, in []byte) { checkParseSame(t, in) })
}

type chanPacketConn struct {
	net.Conn // unused methods
	ch       chan []byte
}

func (c *chanPacketConn) Read(p []byte) (int, error) {
	b, ok := <-c.ch
	if !ok {
		return 0, io.EOF
	}
	return copy(p, b), nil
}

func (c *chanPacketConn) ReadPacket() ([]byte, error) {
	b, ok := <-c.ch
	if !ok {
		return nil, io.EOF
	}
	return b, nil
}

type plainConn struct{ c *chanPacketConn } // Read only: the reader falls back to copying

func (c plainConn) Read(p []byte) (int, error) { return c.c.Read(p) }

type testCounter struct{ v int64 }

func (c *testCounter) Value() int64      { return c.v }
func (c *testCounter) Set(v int64) int64 { o := c.v; c.v = v; return o }
func (c *testCounter) Add(v int64) int64 { o := c.v; c.v += v; return o }

func readAll(t *testing.T, r *UDPReader) (addrs []string, payloads []string) {
	t.Helper()
	for {
		mb, err := r.ReadMultiBuffer()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range mb {
			addrs = append(addrs, b.UDP.String())
			payloads = append(payloads, string(b.Bytes()))
			b.UDP.Address = net.DomainAddress("mutated.invalid") // freedom may rewrite the destination
			b.UDP.Port = 1
			b.Release()
		}
	}
}

func TestSpofyUDPReader(t *testing.T) {
	type pkt struct{ addr, data string }
	in := []pkt{
		{"203.0.113.7:443", "a"},
		{"203.0.113.7:443", "b"},
		{"[2001:db8::1]:53", "c"},
		{"example.com:3478", "d"},
		{"example.com:3478", "e"},
		{"bad address", "dropped"},
		{"203.0.113.7:443", "f"},
		{"203.0.113.7:444", "g"},
	}
	want := []string{
		"udp:203.0.113.7:443", "udp:203.0.113.7:443", "udp:[2001:db8::1]:53",
		"udp:example.com:3478", "udp:example.com:3478", "udp:203.0.113.7:443", "udp:203.0.113.7:444",
	}
	wantData := []string{"a", "b", "c", "d", "e", "f", "g"}

	for _, mode := range []string{"packet", "counter", "copy"} {
		t.Run(mode, func(t *testing.T) {
			c := &chanPacketConn{ch: make(chan []byte, 64)}
			var total int64
			for _, p := range in {
				b := serializedUDPMessage(t, p.addr, len(p.data))
				copy(b[len(b)-len(p.data):], p.data)
				total += int64(len(b))
				c.ch <- b
			}
			// a packet in three fragments in the middle of another flow
			big := &UDPMessage{PacketID: 7, FragCount: 1, Addr: "198.51.100.1:5000", Data: []byte(strings.Repeat("0123456789", 30))}
			for _, f := range FragUDPMessage(big, 140) {
				b := make([]byte, f.Size())
				f.Serialize(b)
				total += int64(len(b))
				c.ch <- b
			}
			c.ch <- serializedUDPMessage(t, "203.0.113.7:443", 2)
			total += int64(len(serializedUDPMessage(t, "203.0.113.7:443", 2)))
			close(c.ch)

			var r *UDPReader
			counter := &testCounter{}
			switch mode {
			case "packet":
				r = newUDPReader(c)
				if r.packets == nil {
					t.Fatal("no fast path for InterConn-like conn")
				}
			case "counter":
				r = newUDPReader(&stat.CounterConnection{Connection: c, ReadCounter: counter})
				if r.packets == nil || r.counter == nil {
					t.Fatal("no fast path through CounterConnection")
				}
			case "copy":
				r = newUDPReader(plainConn{c})
				if r.packets != nil {
					t.Fatal("fast path for a plain reader")
				}
			}
			addrs, data := readAll(t, r)
			w := append(append([]string{}, want...), "udp:198.51.100.1:5000", "udp:203.0.113.7:443")
			wd := append(append([]string{}, wantData...), string(big.Data), "\x00\x00")
			if strings.Join(addrs, "|") != strings.Join(w, "|") {
				t.Fatalf("addrs\n got %q\nwant %q", addrs, w)
			}
			if strings.Join(data, "|") != strings.Join(wd, "|") {
				t.Fatalf("data\n got %q\nwant %q", data, wd)
			}
			if mode == "counter" && counter.v != total {
				t.Fatalf("counter %d, want %d", counter.v, total)
			}
		})
	}
}

type captureWriter struct{ msgs []*UDPMessage }

func (w *captureWriter) Write(p []byte) (int, error) {
	m, err := ParseUDPMessage(append([]byte{}, p...))
	if err != nil {
		return 0, err
	}
	w.msgs = append(w.msgs, m)
	return len(p), nil
}

func TestSpofyUDPWriterAddr(t *testing.T) {
	cw := &captureWriter{}
	w := &UDPWriter{writer: cw, addr: "default.invalid:1"}
	shared := &net.Destination{Network: net.Network_UDP, Address: net.IPAddress([]byte{203, 0, 113, 7}), Port: 443}
	dests := []*net.Destination{
		{Network: net.Network_UDP, Address: net.IPAddress([]byte{203, 0, 113, 7}), Port: 443},
		{Network: net.Network_UDP, Address: net.IPAddress([]byte{203, 0, 113, 7}), Port: 443},
		{Network: net.Network_UDP, Address: net.IPAddress([]byte{203, 0, 113, 8}), Port: 443},
		{Network: net.Network_UDP, Address: net.ParseAddress("2001:db8::1"), Port: 53},
		{Network: net.Network_UDP, Address: net.DomainAddress("example.com"), Port: 53},
		nil,
		shared, shared, shared,
	}
	want := []string{
		"203.0.113.7:443", "203.0.113.7:443", "203.0.113.8:443", "[2001:db8::1]:53", "example.com:53",
		"default.invalid:1", "203.0.113.7:443", "203.0.113.7:444", "example.org:444",
	}
	for i, d := range dests {
		if i == 7 {
			shared.Port = 444 // the same object changed in place
		}
		if i == 8 {
			shared.Address = net.DomainAddress("example.org")
		}
		b := buf.New()
		b.WriteString("x")
		b.UDP = d
		if err := w.WriteMultiBuffer(buf.MultiBuffer{b}); err != nil {
			t.Fatal(err)
		}
	}
	for i, m := range cw.msgs {
		if m.Addr != want[i] {
			t.Fatalf("packet %d: addr %q, want %q", i, m.Addr, want[i])
		}
	}
	if len(cw.msgs) != len(want) {
		t.Fatalf("%d packets, want %d", len(cw.msgs), len(want))
	}
}
