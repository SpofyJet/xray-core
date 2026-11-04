package mux

// From Jolymmiles/Xray-core 8e56c2e0 (adapted: upstream has no newServer helper).

import (
	"context"
	"testing"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	X "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/pipe"
)

type recordingLinkDispatcher struct {
	routing.Dispatcher
	destinations []X.Destination
}

func (d *recordingLinkDispatcher) Dispatch(_ context.Context, destination X.Destination) (*transport.Link, error) {
	d.destinations = append(d.destinations, destination)
	reader, writer := pipe.New(pipe.WithoutSizeLimit())
	return &transport.Link{Reader: reader, Writer: writer}, nil
}

func (d *recordingLinkDispatcher) DispatchLink(_ context.Context, destination X.Destination, _ *transport.Link) error {
	d.destinations = append(d.destinations, destination)
	return nil
}

// Mux.Cool carriers are TCP streams. A UDP packet addressed to v1.mux.cool,
// for example over a Hysteria UDP session, must be dispatched as a packet
// instead of starting a worker whose session handlers outlive the packet link.
func TestServerDoesNotStartMuxCoolOverUDP(t *testing.T) {
	destination := X.UDPDestination(muxCoolAddress, muxCoolPort)
	ctx := session.ContextWithInbound(context.Background(), new(session.Inbound))

	t.Run("DispatchLink", func(t *testing.T) {
		dispatcher := new(recordingLinkDispatcher)
		server := &Server{dispatcher: dispatcher}
		reader, writer := pipe.New(pipe.WithoutSizeLimit())
		// A closed carrier lets a wrongly started worker finish instead of
		// blocking DispatchLink.
		writer.Close()
		link := &transport.Link{Reader: reader, Writer: buf.Discard}
		if err := server.DispatchLink(ctx, destination, link); err != nil {
			t.Fatal(err)
		}
		if len(dispatcher.destinations) != 1 || dispatcher.destinations[0] != destination {
			t.Fatalf("underlying DispatchLink destinations = %v, want [%v]", dispatcher.destinations, destination)
		}
	})

	t.Run("Dispatch", func(t *testing.T) {
		dispatcher := new(recordingLinkDispatcher)
		server := &Server{dispatcher: dispatcher}
		if _, err := server.Dispatch(ctx, destination); err != nil {
			t.Fatal(err)
		}
		if len(dispatcher.destinations) != 1 || dispatcher.destinations[0] != destination {
			t.Fatalf("underlying Dispatch destinations = %v, want [%v]", dispatcher.destinations, destination)
		}
	})
}

// Real Mux.Cool carriers (TCP) must still start a worker.
func TestSpofyServerStillStartsMuxCoolOverTCP(t *testing.T) {
	dispatcher := new(recordingLinkDispatcher)
	server := &Server{dispatcher: dispatcher}
	ctx := session.ContextWithInbound(context.Background(), new(session.Inbound))
	link, err := server.Dispatch(ctx, X.TCPDestination(muxCoolAddress, muxCoolPort))
	if err != nil || link == nil {
		t.Fatalf("Dispatch over TCP: link=%v err=%v", link, err)
	}
	if len(dispatcher.destinations) != 0 {
		t.Fatalf("TCP Mux.Cool carrier was passed through to the dispatcher: %v", dispatcher.destinations)
	}
	common.Close(link.Writer)
}
