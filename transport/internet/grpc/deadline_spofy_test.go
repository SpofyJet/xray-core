package grpc_test

import (
	"context"
	"testing"
	"time"

	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/testing/servers/tcp"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/grpc"
	"github.com/xtls/xray-core/transport/internet/stat"
)

func handshakeThenEcho(handshake chan<- error) internet.ConnHandler {
	return func(c stat.Connection) {
		go func() {
			defer c.Close()
			c.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
			b := make([]byte, 1)
			if _, err := c.Read(b); err != nil {
				handshake <- err
				return
			}
			c.SetReadDeadline(time.Time{})
			handshake <- nil
			for {
				if _, err := c.Read(b); err != nil {
					return
				}
				if _, err := c.Write(b); err != nil {
					return
				}
			}
		}()
	}
}

func readWithin(c net.Conn, d time.Duration) (byte, error) {
	res := make(chan error, 1)
	b := make([]byte, 1)
	go func() {
		_, err := c.Read(b)
		res <- err
	}()
	select {
	case err := <-res:
		return b[0], err
	case <-time.After(d):
		return 0, context.DeadlineExceeded
	}
}

func TestSpofyServerStreamClosedWhenHandshakeDeadlinePasses(t *testing.T) {
	for _, multi := range []bool{false, true} {
		port := tcp.PickPort()
		settings := &internet.MemoryStreamConfig{ProtocolName: "grpc", ProtocolSettings: &grpc.Config{ServiceName: "spofy", MultiMode: multi}}
		handshake := make(chan error, 2)
		ln, err := grpc.Listen(context.Background(), net.LocalHostIP, port, settings, handshakeThenEcho(handshake))
		if err != nil {
			t.Fatal(err)
		}
		dest := net.TCPDestination(net.LocalHostIP, port)

		silent, err := grpc.Dial(context.Background(), dest, settings)
		if err != nil {
			t.Fatal(err)
		}
		active, err := grpc.Dial(context.Background(), dest, settings)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := active.Write([]byte{1}); err != nil {
			t.Fatal(err)
		}

		if _, err := readWithin(silent, 3*time.Second); err == context.DeadlineExceeded {
			t.Fatalf("multi=%v: silent stream still open 3 s after a 300 ms handshake deadline", multi)
		}

		time.Sleep(700 * time.Millisecond)
		if _, err := active.Write([]byte{42}); err != nil {
			t.Fatalf("multi=%v: write after handshake: %v", multi, err)
		}
		if b, err := readWithin(active, 3*time.Second); err != nil || b != 42 {
			t.Fatalf("multi=%v: stream that finished its handshake was cut: %v", multi, err)
		}

		got := []error{<-handshake, <-handshake}
		if (got[0] == nil) == (got[1] == nil) {
			t.Fatalf("multi=%v: want one handshake ok and one failed, got %v", multi, got)
		}
		silent.Close()
		active.Close()
		ln.Close()
	}
}
